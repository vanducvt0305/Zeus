package source

import (
	"context"
	"log"

	"github.com/vanducvt0305/zeus/internal/model"
)

// Multi fans out to several sources and concatenates their records. Duplicates
// across sources are expected — the indexer's identity-resolution stage
// (package resolve) merges them afterwards.
type Multi struct {
	sources []Source
}

// NewMulti combines several sources into one.
func NewMulti(sources ...Source) *Multi {
	return &Multi{sources: sources}
}

func (m *Multi) Name() string { return "multi" }

// Fetch pulls up to limit records from each underlying source. The limit is
// therefore per-source; the combined result (before dedup) may be larger.
func (m *Multi) Fetch(ctx context.Context, limit int) ([]model.MCP, error) {
	var out []model.MCP
	for _, s := range m.sources {
		mcps, err := s.Fetch(ctx, limit)
		if err != nil {
			// Best-effort: one failing source must not sink the others.
			log.Printf("multi: source %q failed: %v", s.Name(), err)
			continue
		}
		out = append(out, mcps...)
	}
	return out, nil
}
