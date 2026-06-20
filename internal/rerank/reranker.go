// Package rerank re-orders the top-K candidates from first-stage retrieval.
//
// First-stage retrieval (dense + sparse) is fast but compares a query against
// each candidate independently, through a single vector or term overlap. A
// reranker instead scores the query and a candidate *together*, reading the
// candidate's full text, which is a much stronger relevance signal. This is the
// classic retrieve-then-rerank pattern: cast a wide, cheap net, then spend more
// compute precisely ranking the small shortlist.
package rerank

import (
	"context"

	"github.com/vanducvt0305/zeus/internal/store"
)

// Reranker re-orders hits for a query, best first. It receives the first-stage
// shortlist and returns a (possibly re-ordered, possibly trimmed) list.
type Reranker interface {
	Rerank(ctx context.Context, query string, hits []store.Hit) ([]store.Hit, error)
	Name() string
}

// Noop returns the hits unchanged — the baseline for measuring a reranker's
// contribution.
type Noop struct{}

func (Noop) Name() string { return "noop" }

func (Noop) Rerank(_ context.Context, _ string, hits []store.Hit) ([]store.Hit, error) {
	return hits, nil
}
