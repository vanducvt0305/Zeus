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
	Sparse    sparse.Encoder
	Store     store.Store

	// BatchSize bounds how many texts are embedded per request.
	BatchSize int
	// ExtractConcurrency bounds how many servers are probed for tools at once.
	ExtractConcurrency int
}

// New builds an Indexer with sensible defaults. Nil stages default to no-ops: a
// nil extractor/enricher skips that stage; a nil sparse encoder means
// dense-only points.
func New(src source.Source, ext extract.Extractor, enr enrich.Enricher, emb embed.Embedder, sp sparse.Encoder, st store.Store) *Indexer {
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
	}
}

// Run fetches up to limit MCPs from the source and indexes them. A limit <= 0
// means "index everything the source exposes".
func (ix *Indexer) Run(ctx context.Context, limit int) (int, error) {
	if ix.BatchSize <= 0 {
		ix.BatchSize = 64
	}

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

	queue := buildRecords(mcps)

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
			if ix.Sparse != nil {
				records[i].Sparse = ix.Sparse.Encode(batch[i].text)
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

// enrichAll runs the enricher over every MCP. Enrichment is best-effort: a
// failure on one record logs a warning and keeps whatever (heuristic) card the
// enricher returned, rather than dropping the record.
func (ix *Indexer) enrichAll(ctx context.Context, mcps []model.MCP) []model.MCP {
	log.Printf("enriching %d MCPs with %q...", len(mcps), ix.Enricher.Name())
	out := make([]model.MCP, len(mcps))
	failures := 0
	for i, m := range mcps {
		enriched, err := ix.Enricher.Enrich(ctx, m)
		if err != nil {
			failures++
			if failures <= 5 {
				log.Printf("warning: %v", err)
			}
		}
		out[i] = enriched
	}
	if failures > 0 {
		log.Printf("enrichment completed with %d failures (used fallback)", failures)
	}
	return out
}

// scoreTrustAll assigns each MCP a trust prior. Best-effort: a failure keeps
// whatever (deterministic) score the scorer returned.
func (ix *Indexer) scoreTrustAll(ctx context.Context, mcps []model.MCP) []model.MCP {
	scorer := ix.Trust
	if scorer == nil {
		scorer = trust.Noop{}
	}
	if _, isNoop := scorer.(trust.Noop); isNoop {
		return mcps
	}
	log.Printf("scoring trust for %d MCPs with %q...", len(mcps), scorer.Name())
	out := make([]model.MCP, len(mcps))
	failures := 0
	for i, m := range mcps {
		scored, err := scorer.Score(ctx, m)
		if err != nil {
			failures++
			if failures <= 5 {
				log.Printf("warning: %v", err)
			}
		}
		out[i] = scored
	}
	if failures > 0 {
		log.Printf("trust scoring completed with %d failures (used fallback)", failures)
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
