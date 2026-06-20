// Package trust assigns each MCP a 0..1 quality/trust prior at index time.
// Search blends this prior with relevance so that, among comparably-relevant
// results, the more trustworthy server surfaces first — the curation layer that
// keeps low-quality or abandoned servers from outranking good ones.
//
// The offline Heuristic scorer uses objective signals (popularity, recency,
// completeness). The LLM scorer adds a model's judgment of clarity and
// legitimacy on top. Both are best-effort and never fatal to indexing.
package trust

import (
	"context"
	"math"
	"time"

	"github.com/vanducvt0305/zeus/internal/model"
)

// Scorer assigns a trust prior to an MCP, returning the record with Trust
// (and optionally TrustRationale/TrustFlags) set.
type Scorer interface {
	Score(ctx context.Context, m model.MCP) (model.MCP, error)
	Name() string
}

// Noop leaves trust unset (0), so ranking is driven purely by relevance. It is
// the baseline for measuring how much trust scoring changes results.
type Noop struct{}

func (Noop) Name() string { return "noop" }

func (Noop) Score(_ context.Context, m model.MCP) (model.MCP, error) { return m, nil }

// Heuristic scores from objective, offline signals.
type Heuristic struct{}

func (Heuristic) Name() string { return "heuristic" }

func (Heuristic) Score(_ context.Context, m model.MCP) (model.MCP, error) {
	m.Trust = deterministic(m)
	return m, nil
}

// deterministic combines popularity, recency, and completeness into 0..1.
func deterministic(m model.MCP) float64 {
	score := 0.4*popularityScore(m.Popularity) +
		0.3*recencyScore(m.UpdatedAt) +
		0.3*completenessScore(m)
	return clamp01(score)
}

// popularityScore log-scales stars: ~10k stars approaches 1.0.
func popularityScore(stars int) float64 {
	if stars <= 0 {
		return 0
	}
	return clamp01(math.Log10(float64(stars)+1) / 4.0)
}

// recencyScore is 1 for servers updated within ~90 days, decaying to 0 at ~2
// years. Unknown/unparseable dates are treated as neutral (0.5).
func recencyScore(updatedAt string) float64 {
	if updatedAt == "" {
		return 0.5
	}
	t, err := time.Parse(time.RFC3339, updatedAt)
	if err != nil {
		return 0.5
	}
	days := time.Since(t).Hours() / 24
	switch {
	case days <= 90:
		return 1
	case days >= 730:
		return 0
	default:
		return 1 - (days-90)/(730-90)
	}
}

// completenessScore rewards records that carry the signals an agent needs to
// actually use the server.
func completenessScore(m model.MCP) float64 {
	have := 0
	if m.Description != "" || m.Enrichment.Summary != "" {
		have++
	}
	if len(m.Tools) > 0 {
		have++
	}
	if len(m.Transports) > 0 || len(m.Packages) > 0 {
		have++ // there is a concrete way to run/connect to it
	}
	if len(m.AllCategories()) > 0 {
		have++
	}
	return float64(have) / 4.0
}

func clamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}
