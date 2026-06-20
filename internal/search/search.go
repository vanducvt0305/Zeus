// Package search is the query-time pipeline shared by the MCP server and the
// evaluation harness: embed the query (dense, and sparse for hybrid), retrieve
// a candidate pool from the store, then rerank and truncate to top-k. Keeping
// it in one place guarantees the server and the evaluator score identically.
package search

import (
	"context"
	"fmt"

	"github.com/vanducvt0305/zeus/internal/embed"
	"github.com/vanducvt0305/zeus/internal/rerank"
	"github.com/vanducvt0305/zeus/internal/sparse"
	"github.com/vanducvt0305/zeus/internal/store"
)

// Service runs the retrieve-then-rerank pipeline.
type Service struct {
	Embedder embed.Embedder
	Sparse   sparse.Encoder  // used only when Hybrid is true
	Store    store.Store
	Reranker rerank.Reranker // nil means no reranking

	// Hybrid fuses sparse keyword retrieval with dense retrieval.
	Hybrid bool
	// Pool is the first-stage candidate count handed to the reranker before
	// truncating to top-k.
	Pool int
}

// Search returns up to topK MCPs for the query.
func (s *Service) Search(ctx context.Context, query string, topK int, filter store.Filter) ([]store.Hit, error) {
	if topK <= 0 {
		topK = 5
	}
	pool := s.Pool
	if pool < topK {
		pool = topK
	}

	vecs, err := s.Embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("embedding query: %w", err)
	}

	sq := store.SearchQuery{Dense: vecs[0], TopK: pool, Filter: filter}
	if s.Hybrid && s.Sparse != nil {
		sq.Sparse = s.Sparse.Encode(query)
	}

	hits, err := s.Store.Search(ctx, sq)
	if err != nil {
		return nil, err
	}

	// Rerank is best-effort: its implementations fall back internally and always
	// return a usable ordering, so a reranker error never loses results.
	if s.Reranker != nil {
		reranked, _ := s.Reranker.Rerank(ctx, query, hits)
		if len(reranked) > 0 {
			hits = reranked
		}
	}

	if len(hits) > topK {
		hits = hits[:topK]
	}
	return hits, nil
}
