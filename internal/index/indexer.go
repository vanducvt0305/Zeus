// Package index wires a Source, an Embedder, and a Store together: it pulls
// MCP records from a catalog, turns them into vectors, and writes them to the
// store. Run it periodically (cron, CI, a queue) to keep the index fresh.
package index

import (
	"context"
	"fmt"
	"log"

	"github.com/vanducvt0305/zeus/internal/embed"
	"github.com/vanducvt0305/zeus/internal/model"
	"github.com/vanducvt0305/zeus/internal/source"
	"github.com/vanducvt0305/zeus/internal/store"
)

// Indexer orchestrates one indexing pass.
type Indexer struct {
	Source   source.Source
	Embedder embed.Embedder
	Store    store.Store

	// BatchSize bounds how many texts are embedded per request.
	BatchSize int
}

// New builds an Indexer with sensible defaults.
func New(src source.Source, emb embed.Embedder, st store.Store) *Indexer {
	return &Indexer{Source: src, Embedder: emb, Store: st, BatchSize: 64}
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

	if err := ix.Store.EnsureCollection(ctx, ix.Embedder.Dim()); err != nil {
		return 0, fmt.Errorf("ensuring collection: %w", err)
	}

	// Build one server-level record per MCP plus one tool-level record per
	// tool, then embed and upsert in batches.
	type pending struct {
		rec  store.Record
		text string
	}
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
		}
		if err := ix.Store.Upsert(ctx, records); err != nil {
			return indexed, fmt.Errorf("upserting batch: %w", err)
		}
		indexed += len(records)
		log.Printf("indexed %d/%d points", indexed, len(queue))
	}

	return countServers(mcps), nil
}

func countServers(mcps []model.MCP) int { return len(mcps) }
