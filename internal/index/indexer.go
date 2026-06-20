// Package index wires a Source, an Enricher, an Embedder, and a Store together:
// it pulls MCP records from a catalog, rewrites them into capability cards,
// turns them into vectors, and writes them to the store. Run it periodically
// (cron, CI, a queue) to keep the index fresh.
package index

import (
	"context"
	"fmt"
	"log"

	"github.com/vanducvt0305/zeus/internal/embed"
	"github.com/vanducvt0305/zeus/internal/enrich"
	"github.com/vanducvt0305/zeus/internal/model"
	"github.com/vanducvt0305/zeus/internal/source"
	"github.com/vanducvt0305/zeus/internal/sparse"
	"github.com/vanducvt0305/zeus/internal/store"
)

// Indexer orchestrates one indexing pass: source → enrich → embed → store.
type Indexer struct {
	Source   source.Source
	Enricher enrich.Enricher
	Embedder embed.Embedder
	Sparse   sparse.Encoder
	Store    store.Store

	// BatchSize bounds how many texts are embedded per request.
	BatchSize int
}

// New builds an Indexer with sensible defaults. A nil enricher means no
// enrichment is applied; a nil sparse encoder means dense-only points.
func New(src source.Source, enr enrich.Enricher, emb embed.Embedder, sp sparse.Encoder, st store.Store) *Indexer {
	if enr == nil {
		enr = enrich.Noop{}
	}
	return &Indexer{Source: src, Enricher: enr, Embedder: emb, Sparse: sp, Store: st, BatchSize: 64}
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

	mcps = ix.enrichAll(ctx, mcps)

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
