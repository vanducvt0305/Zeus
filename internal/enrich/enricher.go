// Package enrich turns a raw MCP record into an enriched one carrying a
// "capability card": a normalized summary, the tasks it accomplishes phrased in
// agent-intent language, synthetic example queries, synonyms, and categories.
//
// This is the highest-leverage stage in the system. Search quality is bounded
// by how well each MCP is *represented*; enrichment rewrites terse, marketing
// metadata into the language agents actually search in, closing the
// query/document vocabulary gap.
package enrich

import (
	"context"

	"github.com/vanducvt0305/zeus/internal/model"
)

// Enricher augments an MCP with an Enrichment capability card.
type Enricher interface {
	// Enrich returns a copy of m with m.Enrichment populated. It must be
	// best-effort: on partial failure it should still return a usable record
	// rather than erroring the whole pipeline.
	Enrich(ctx context.Context, m model.MCP) (model.MCP, error)
	// Name identifies the enricher, recorded on each card for debugging.
	Name() string
}

// Noop returns records unchanged. Useful as a baseline in evaluation, to
// measure exactly how much enrichment moves the metrics.
type Noop struct{}

func (Noop) Name() string { return "noop" }

func (Noop) Enrich(_ context.Context, m model.MCP) (model.MCP, error) {
	return m, nil
}
