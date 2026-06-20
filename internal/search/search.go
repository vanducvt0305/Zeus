// Package search is the query-time pipeline shared by the MCP server and the
// evaluation harness: embed the query (dense, and sparse for hybrid), retrieve
// a candidate pool from the store, then rerank and truncate to top-k. Keeping
// it in one place guarantees the server and the evaluator score identically.
package search

import (
	"context"
	"fmt"
	"sort"

	"github.com/vanducvt0305/zeus/internal/embed"
	"github.com/vanducvt0305/zeus/internal/rerank"
	"github.com/vanducvt0305/zeus/internal/sparse"
	"github.com/vanducvt0305/zeus/internal/store"
)

// Service runs the retrieve → rerank → trust-blend pipeline.
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
	// TrustWeight (0..1) blends each result's stored trust prior into the final
	// ranking, so better servers win among comparably-relevant ones. 0 disables.
	TrustWeight float64
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

	hits = s.blendTrust(hits)

	if len(hits) > topK {
		hits = hits[:topK]
	}
	return hits, nil
}

// blendTrust nudges the relevance ranking with each result's trust prior:
//
//	final = (1-w)*relevance + w*trust
//
// relevance is a rank-based score (1 at the top, decaying down the list), so a
// modest weight only reorders among comparably-relevant results rather than
// overriding relevance. With w=0 or uniform trust, the order is unchanged.
func (s *Service) blendTrust(hits []store.Hit) []store.Hit {
	if s.TrustWeight <= 0 || len(hits) < 2 {
		return hits
	}
	w := s.TrustWeight
	n := float64(len(hits))
	type scored struct {
		hit   store.Hit
		final float64
	}
	ranked := make([]scored, len(hits))
	for i, h := range hits {
		relevance := 1 - float64(i)/n
		ranked[i] = scored{hit: h, final: (1-w)*relevance + w*h.MCP.Trust}
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].final > ranked[j].final })
	out := make([]store.Hit, len(ranked))
	for i, r := range ranked {
		out[i] = r.hit
	}
	return out
}
