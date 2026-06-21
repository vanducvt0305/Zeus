// Package index wires a Source, an Enricher, an Embedder, and a Store together:
// it pulls MCP records from a catalog, rewrites them into capability cards,
// turns them into vectors, and writes them to the store. Run it periodically
// (cron, CI, a queue) to keep the index fresh.
package index

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/vanducvt0305/zeus/internal/embed"
	"github.com/vanducvt0305/zeus/internal/enrich"
	"github.com/vanducvt0305/zeus/internal/extract"
	"github.com/vanducvt0305/zeus/internal/model"
	"github.com/vanducvt0305/zeus/internal/resolve"
	"github.com/vanducvt0305/zeus/internal/source"
	"github.com/vanducvt0305/zeus/internal/sparse"
	"github.com/vanducvt0305/zeus/internal/store"
	"github.com/vanducvt0305/zeus/internal/trust"
)

// Indexer orchestrates one indexing pass: source → extract tools → enrich →
// embed(+sparse) → store.
type Indexer struct {
	Source    source.Source
	Extractor extract.Extractor
	Enricher  enrich.Enricher
	Trust     trust.Scorer
	Embedder  embed.Embedder
	Sparse    sparse.Fitter
	Store     store.Store

	// BatchSize bounds how many texts are embedded per request.
	BatchSize int
	// ExtractConcurrency bounds how many servers are probed for tools at once.
	ExtractConcurrency int
	// Concurrency bounds the enrichment and trust stages, which are
	// LLM/network-bound and otherwise painfully slow when run serially.
	Concurrency int
	// Prune deletes a (re)indexed MCP's existing points before upserting, so
	// tools/queries removed since the last run don't linger as orphans.
	Prune bool
}

// New builds an Indexer with sensible defaults. Nil stages default to no-ops: a
// nil extractor/enricher skips that stage; a nil sparse fitter means dense-only
// points.
func New(src source.Source, ext extract.Extractor, enr enrich.Enricher, emb embed.Embedder, sp sparse.Fitter, st store.Store) *Indexer {
	if ext == nil {
		ext = extract.Noop{}
	}
	if enr == nil {
		enr = enrich.Noop{}
	}
	return &Indexer{
		Source:             src,
		Extractor:          ext,
		Enricher:           enr,
		Embedder:           emb,
		Sparse:             sp,
		Store:              st,
		BatchSize:          64,
		ExtractConcurrency: 8,
		Concurrency:        8,
		Prune:              true,
	}
}

// Run fetches up to limit MCPs from the source and indexes them. A limit <= 0
// means "index everything the source exposes".
func (ix *Indexer) Run(ctx context.Context, limit int) (int, error) {
	if ix.BatchSize <= 0 {
		ix.BatchSize = 64
	}
	start := time.Now()
	defer func() { log.Printf("indexing pass took %s", time.Since(start).Round(time.Millisecond)) }()

	log.Printf("fetching MCPs from source %q...", ix.Source.Name())
	mcps, err := ix.Source.Fetch(ctx, limit)
	if err != nil {
		return 0, fmt.Errorf("fetching from source: %w", err)
	}
	log.Printf("fetched %d MCPs", len(mcps))

	if resolved := resolve.Dedup(mcps); len(resolved) < len(mcps) {
		log.Printf("identity resolution: merged %d records into %d distinct MCPs", len(mcps), len(resolved))
		mcps = resolved
	}

	mcps = ix.extractAll(ctx, mcps)
	mcps = ix.enrichAll(ctx, mcps)
	mcps = ix.scoreTrustAll(ctx, mcps)

	if err := ix.Store.EnsureCollection(ctx, ix.Embedder.Dim()); err != nil {
		return 0, fmt.Errorf("ensuring collection: %w", err)
	}

	if ix.Prune {
		ids := distinctIDs(mcps)
		if err := ix.Store.DeleteByMCPs(ctx, ids); err != nil {
			return 0, fmt.Errorf("pruning stale points: %w", err)
		}
	}

	queue := buildRecords(mcps)

	// Fit the sparse encoder over the whole corpus before encoding any document.
	// For plain TF this is a no-op; for BM25 it computes document frequencies and
	// the average document length (and persists them so the server's query side
	// applies matching IDF weights). On failure we fall back to dense-only points
	// rather than storing inconsistent sparse vectors.
	var sparseEnc sparse.Encoder
	if ix.Sparse != nil {
		texts := make([]string, len(queue))
		for i, p := range queue {
			texts[i] = p.text
		}
		enc, err := ix.Sparse.Fit(texts)
		if err != nil {
			log.Printf("warning: sparse fit (%s) failed: %v; indexing dense-only", ix.Sparse.Name(), err)
		} else {
			sparseEnc = enc
			log.Printf("sparse encoder %q fitted over %d documents", enc.Name(), len(texts))
		}
	}

	indexed := 0
	for start := 0; start < len(queue); start += ix.BatchSize {
		end := min(start+ix.BatchSize, len(queue))
		batch := queue[start:end]

		texts := make([]string, len(batch))
		for i, p := range batch {
			texts[i] = p.text
		}
		vectors, err := ix.Embedder.Embed(ctx, texts)
		if err != nil {
			return indexed, fmt.Errorf("embedding batch: %w", err)
		}

		records := make([]store.Record, len(batch))
		for i := range batch {
			records[i] = batch[i].rec
			records[i].Vector = vectors[i]
			if sparseEnc != nil {
				records[i].Sparse = sparseEnc.EncodeDoc(batch[i].text)
			}
		}
		if err := ix.Store.Upsert(ctx, records); err != nil {
			return indexed, fmt.Errorf("upserting batch: %w", err)
		}
		indexed += len(records)
		log.Printf("indexed %d/%d points", indexed, len(queue))
	}

	return len(mcps), nil
}

// extractAll probes servers for their tool lists concurrently. Only records
// that lack tools and expose a connectable transport are probed; everything
// else passes through untouched. Extraction is best-effort — most public
// servers require auth — so failures are counted, not fatal.
func (ix *Indexer) extractAll(ctx context.Context, mcps []model.MCP) []model.MCP {
	if _, isNoop := ix.Extractor.(extract.Noop); isNoop {
		return mcps
	}
	conc := ix.ExtractConcurrency
	if conc <= 0 {
		conc = 8
	}
	log.Printf("extracting tools from up to %d MCPs with %q (concurrency=%d)...", len(mcps), ix.Extractor.Name(), conc)

	out := make([]model.MCP, len(mcps))
	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var withTools, failed int

	for i, m := range mcps {
		// Skip records that already have tools or have nothing to connect to.
		if len(m.Tools) > 0 || len(m.Transports) == 0 {
			out[i] = m
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, m model.MCP) {
			defer wg.Done()
			defer func() { <-sem }()
			em, err := ix.Extractor.Extract(ctx, m)
			out[i] = em
			mu.Lock()
			switch {
			case err != nil:
				failed++
			case len(em.Tools) > 0:
				withTools++
			}
			mu.Unlock()
		}(i, m)
	}
	wg.Wait()

	log.Printf("tool extraction: %d servers yielded tools, %d failed/unreachable", withTools, failed)
	return out
}

// enrichAll runs the enricher over every MCP concurrently. Best-effort: a
// failure logs a warning and keeps whatever (heuristic) fallback the enricher
// returned, rather than dropping the record.
func (ix *Indexer) enrichAll(ctx context.Context, mcps []model.MCP) []model.MCP {
	log.Printf("enriching %d MCPs with %q (concurrency=%d)...", len(mcps), ix.Enricher.Name(), ix.concurrency())
	return ix.concurrentMap(ctx, mcps, "enrichment", ix.Enricher.Enrich)
}

// scoreTrustAll assigns each MCP a trust prior concurrently. Best-effort.
func (ix *Indexer) scoreTrustAll(ctx context.Context, mcps []model.MCP) []model.MCP {
	scorer := ix.Trust
	if scorer == nil {
		scorer = trust.Noop{}
	}
	if _, isNoop := scorer.(trust.Noop); isNoop {
		return mcps
	}
	log.Printf("scoring trust for %d MCPs with %q (concurrency=%d)...", len(mcps), scorer.Name(), ix.concurrency())
	return ix.concurrentMap(ctx, mcps, "trust scoring", scorer.Score)
}

func (ix *Indexer) concurrency() int {
	if ix.Concurrency <= 0 {
		return 8
	}
	return ix.Concurrency
}

// concurrentMap applies fn to every MCP with bounded concurrency, preserving
// order. Errors are counted and the (fallback) result fn returned is kept.
func (ix *Indexer) concurrentMap(ctx context.Context, mcps []model.MCP, label string, fn func(context.Context, model.MCP) (model.MCP, error)) []model.MCP {
	out := make([]model.MCP, len(mcps))
	sem := make(chan struct{}, ix.concurrency())
	var wg sync.WaitGroup
	var mu sync.Mutex
	failures := 0
	for i, m := range mcps {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, m model.MCP) {
			defer wg.Done()
			defer func() { <-sem }()
			r, err := fn(ctx, m)
			out[i] = r
			if err != nil {
				mu.Lock()
				failures++
				if failures <= 5 {
					log.Printf("warning: %v", err)
				}
				mu.Unlock()
			}
		}(i, m)
	}
	wg.Wait()
	if failures > 0 {
		log.Printf("%s completed with %d failures (used fallback)", label, failures)
	}
	return out
}

// distinctIDs returns the unique MCP ids in the slice.
func distinctIDs(mcps []model.MCP) []string {
	seen := make(map[string]struct{}, len(mcps))
	out := make([]string, 0, len(mcps))
	for _, m := range mcps {
		if _, ok := seen[m.ID]; ok {
			continue
		}
		seen[m.ID] = struct{}{}
		out = append(out, m.ID)
	}
	return out
}

// pending pairs a record with the text to embed for it.
type pending struct {
	rec  store.Record
	text string
}

// buildRecords expands each MCP into its multi-representation points: one
// server vector, one vector per tool, and one vector per synthetic example
// query. The query points carry agent-intent language directly, which is what
// closes the query/document vocabulary gap.
func buildRecords(mcps []model.MCP) []pending {
	var queue []pending
	for _, m := range mcps {
		queue = append(queue, pending{
			rec:  store.Record{Kind: store.KindServer, MCP: m},
			text: m.EmbeddingText(),
		})
		for _, t := range m.Tools {
			queue = append(queue, pending{
				rec:  store.Record{Kind: store.KindTool, ToolName: t.Name, MCP: m},
				text: m.ToolEmbeddingText(t),
			})
		}
		for _, q := range m.Enrichment.ExampleQueries {
			queue = append(queue, pending{
				rec:  store.Record{Kind: store.KindQuery, Query: q, MCP: m},
				text: q,
			})
		}
	}
	return queue
}
