// Package search is the query-time pipeline shared by the MCP server and the
// evaluation harness: embed the query (dense, and sparse for hybrid), retrieve
// a candidate pool from the store, then rerank and truncate to top-k. Keeping
// it in one place guarantees the server and the evaluator score identically.
package search

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/vanducvt0305/zeus/internal/embed"
	"github.com/vanducvt0305/zeus/internal/rerank"
	"github.com/vanducvt0305/zeus/internal/sparse"
	"github.com/vanducvt0305/zeus/internal/store"
	"github.com/vanducvt0305/zeus/internal/usage"
)

// Service runs the retrieve → rerank → trust/usage-blend pipeline.
type Service struct {
	Embedder embed.Embedder
	Sparse   sparse.Encoder  // used only when Hybrid is true
	Store    store.Store
	Reranker rerank.Reranker // nil means no reranking
	Usage    usage.Recorder  // nil means no usage signal

	// Hybrid fuses sparse keyword retrieval with dense retrieval.
	Hybrid bool
	// Pool is the first-stage candidate count handed to the reranker before
	// truncating to top-k.
	Pool int
	// TrustWeight (0..1) blends each result's stored trust prior into ranking.
	TrustWeight float64
	// UsageWeight (0..1) blends the learned usage prior (the flywheel) into
	// ranking, so MCPs agents actually use successfully rise over time.
	UsageWeight float64
	// CoverageWeight (0..1) rewards an MCP that matched on several of its
	// representations (multiple tools / synthetic queries) over one that matched
	// on a single point — a stronger relevance signal the rank-collapse discards.
	CoverageWeight float64

	// Cache memoizes finished results for repeated queries (nil = disabled).
	Cache *resultCache
}

// EnableCache turns on (or off) the query-result cache. A ttl or max <= 0
// disables it. Call once at construction.
func (s *Service) EnableCache(ttl time.Duration, max int) {
	s.Cache = newResultCache(ttl, max)
}

// Search returns up to topK MCPs for the query. Results whose Confidence is
// below minConfidence (0..1) are dropped, so an agent can ask for "only strong
// matches" instead of always receiving topK best-of-whatever-exists; pass 0 to
// disable the cutoff.
func (s *Service) Search(ctx context.Context, query string, topK int, minConfidence float64, filter store.Filter) ([]store.Hit, error) {
	if topK <= 0 {
		topK = 5
	}

	key := cacheKey(query, topK, minConfidence, filter)
	if hits, ok := s.Cache.get(key); ok {
		return hits, nil
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

	hits = s.finalize(hits)

	if minConfidence > 0 {
		kept := hits[:0]
		for _, h := range hits {
			if float64(h.Confidence) >= minConfidence {
				kept = append(kept, h)
			}
		}
		hits = kept
	}

	if len(hits) > topK {
		hits = hits[:topK]
	}

	s.Cache.put(key, hits)
	return hits, nil
}

// finalize computes each result's final score and orders by it. It blends the
// post-rerank relevance with the coverage, trust and usage priors:
//
//	final = wR*relevance + wC*coverage + wT*trust + wU*usage   (wR = 1-wC-wT-wU)
//
// relevance is the reranker's own score (query-term coverage / LLM order),
// min-max normalized across the pool so the gap between a strong top match and a
// weak tail is preserved — unlike a pure rank decay, where every step looks
// equal and a modest trust weight can leapfrog a clearly better match. When the
// scores are degenerate (all equal, e.g. before any reranker has run) it falls
// back to the rank decay so trust still breaks ties among equally-relevant hits.
//
// It also records each hit's absolute Confidence (the pre-blend relevance) so an
// agent can tell a strong match from the best of a weak field, and always
// overwrites Score with the final blended value so the returned ordering and
// score agree.
func (s *Service) finalize(hits []store.Hit) []store.Hit {
	if len(hits) == 0 {
		return hits
	}
	wT, wU, wC := clampWeights(s.TrustWeight, s.UsageWeight, s.CoverageWeight)
	wR := 1 - wT - wU - wC

	rel := relevanceScores(hits)

	type scored struct {
		hit   store.Hit
		final float64
	}
	ranked := make([]scored, len(hits))
	for i, h := range hits {
		var u float64
		if s.Usage != nil {
			u = s.Usage.Score(h.MCP.ID)
		}
		h.Confidence = clamp01f(h.Score) // absolute, pre-blend relevance
		final := wR*rel[i] + wC*coverage(h.MatchCount) + wT*clamp01(h.MCP.Trust) + wU*u
		ranked[i] = scored{hit: h, final: final}
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].final > ranked[j].final })
	out := make([]store.Hit, len(ranked))
	for i, r := range ranked {
		r.hit.Score = float32(r.final)
		out[i] = r.hit
	}
	return out
}

// relevanceScores turns the post-rerank Score of each hit into a 0..1 relevance,
// min-max normalized across the pool. If every score is equal (degenerate), it
// falls back to a rank-based decay (1 at the top) so trust/usage can still break
// ties among hits the retriever considered equally relevant.
func relevanceScores(hits []store.Hit) []float64 {
	n := len(hits)
	out := make([]float64, n)
	minS, maxS := float64(hits[0].Score), float64(hits[0].Score)
	for _, h := range hits {
		v := float64(h.Score)
		if v < minS {
			minS = v
		}
		if v > maxS {
			maxS = v
		}
	}
	if maxS > minS {
		span := maxS - minS
		for i, h := range hits {
			out[i] = (float64(h.Score) - minS) / span
		}
		return out
	}
	for i := range hits {
		out[i] = 1 - float64(i)/float64(n)
	}
	return out
}

// coverage maps the number of matched representations to a saturating 0..1
// bonus: a single match earns nothing (the baseline), and extra matches help
// with diminishing returns (2→0.5, 3→0.67, 4→0.75, …).
func coverage(matchCount int) float64 {
	if matchCount <= 1 {
		return 0
	}
	return 1 - 1/float64(matchCount)
}

// clampWeights keeps each weight in [0,1] and their sum <= 1 (so relevance keeps
// a non-negative share), scaling them down proportionally if they exceed 1.
func clampWeights(wT, wU, wC float64) (float64, float64, float64) {
	wT, wU, wC = clamp01(wT), clamp01(wU), clamp01(wC)
	if sum := wT + wU + wC; sum > 1 {
		wT /= sum
		wU /= sum
		wC /= sum
	}
	return wT, wU, wC
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func clamp01f(v float32) float32 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
