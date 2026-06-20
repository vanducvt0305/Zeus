package trust

import (
	"context"
	"testing"
	"time"

	"github.com/vanducvt0305/zeus/internal/model"
)

func TestHeuristicRanksStrongerHigher(t *testing.T) {
	recent := time.Now().Add(-10 * 24 * time.Hour).Format(time.RFC3339)
	old := time.Now().Add(-3 * 365 * 24 * time.Hour).Format(time.RFC3339)

	strong := model.MCP{
		Description: "Clear capability.",
		Tools:       []model.Tool{{Name: "do_thing"}},
		Transports:  []model.Transport{{Type: "streamable-http", URL: "https://x/mcp"}},
		Categories:  []string{"db"},
		Popularity:  5000,
		UpdatedAt:   recent,
	}
	weak := model.MCP{
		Description: "",
		Popularity:  1,
		UpdatedAt:   old,
	}

	s, _ := Heuristic{}.Score(context.Background(), strong)
	w, _ := Heuristic{}.Score(context.Background(), weak)

	if !(s.Trust > w.Trust) {
		t.Fatalf("strong trust %.3f should exceed weak %.3f", s.Trust, w.Trust)
	}
	if s.Trust < 0 || s.Trust > 1 || w.Trust < 0 || w.Trust > 1 {
		t.Fatalf("trust out of range: strong=%.3f weak=%.3f", s.Trust, w.Trust)
	}
}

func TestNoopLeavesTrustUnset(t *testing.T) {
	m, _ := Noop{}.Score(context.Background(), model.MCP{Popularity: 9999})
	if m.Trust != 0 {
		t.Fatalf("Noop should not set trust, got %.3f", m.Trust)
	}
}

func TestPopularityAndRecencyMonotonic(t *testing.T) {
	if popularityScore(0) != 0 {
		t.Error("zero stars should score 0")
	}
	if !(popularityScore(10000) > popularityScore(100)) {
		t.Error("more stars should score higher")
	}
	recent := time.Now().Add(-1 * 24 * time.Hour).Format(time.RFC3339)
	old := time.Now().Add(-3 * 365 * 24 * time.Hour).Format(time.RFC3339)
	if !(recencyScore(recent) > recencyScore(old)) {
		t.Error("recent should score higher than old")
	}
	if recencyScore("not-a-date") != 0.5 {
		t.Error("unparseable date should be neutral 0.5")
	}
}
