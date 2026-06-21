package search

import (
	"context"
	"testing"

	"github.com/vanducvt0305/zeus/internal/model"
	"github.com/vanducvt0305/zeus/internal/store"
)

func TestBlendTrustPromotesTrustedAmongClose(t *testing.T) {
	// Two near-adjacent results; the second is much more trustworthy. A
	// meaningful weight should promote it above the first.
	hits := []store.Hit{
		{MCP: model.MCP{ID: "low-trust", Trust: 0.1}},
		{MCP: model.MCP{ID: "high-trust", Trust: 0.95}},
	}
	s := &Service{TrustWeight: 0.5}
	out := s.finalize(hits)
	if out[0].MCP.ID != "high-trust" {
		t.Fatalf("expected high-trust promoted to top, got %q", out[0].MCP.ID)
	}
}

func TestBlendTrustDisabledKeepsOrder(t *testing.T) {
	hits := []store.Hit{
		{MCP: model.MCP{ID: "a", Trust: 0.1}},
		{MCP: model.MCP{ID: "b", Trust: 0.99}},
	}
	s := &Service{TrustWeight: 0} // disabled
	out := s.finalize(hits)
	if out[0].MCP.ID != "a" {
		t.Fatalf("with weight 0, order must be unchanged; got %q first", out[0].MCP.ID)
	}
}

func TestBlendTrustSmallWeightRespectsRelevance(t *testing.T) {
	// A small weight must not let a marginally-more-trusted item leapfrog a
	// clearly more relevant one several ranks above it.
	hits := []store.Hit{
		{MCP: model.MCP{ID: "top", Trust: 0.5}},
		{MCP: model.MCP{ID: "mid", Trust: 0.5}},
		{MCP: model.MCP{ID: "bottom", Trust: 0.8}},
	}
	s := &Service{TrustWeight: 0.15}
	out := s.finalize(hits)
	if out[0].MCP.ID != "top" {
		t.Fatalf("small weight should keep the most relevant on top, got %q", out[0].MCP.ID)
	}
}

// TestRelevanceMagnitudeStopsTrustLeapfrog is the #1 fix: when the top match is
// clearly stronger than the rest, a modest trust weight must not promote a
// far-weaker-but-trusted result. With pure rank-decay relevance (every step
// equal), a high-trust item one rank down would leapfrog; magnitude-aware
// relevance keeps the clearly-better match on top.
func TestRelevanceMagnitudeStopsTrustLeapfrog(t *testing.T) {
	hits := []store.Hit{
		{MCP: model.MCP{ID: "strong", Trust: 0.0}, Score: 1.0},
		{MCP: model.MCP{ID: "weak-but-trusted", Trust: 1.0}, Score: 0.3},
		{MCP: model.MCP{ID: "filler-a"}, Score: 0.2},
		{MCP: model.MCP{ID: "filler-b"}, Score: 0.1},
	}
	s := &Service{TrustWeight: 0.15}
	out := s.finalize(hits)
	if out[0].MCP.ID != "strong" {
		t.Fatalf("clearly-stronger match must stay on top, got %q", out[0].MCP.ID)
	}
}

// TestCoverageBonusPromotesBroaderMatch is the #3 fix: among equally-relevant
// results, one that matched on many of its representations (tools/queries)
// should outrank one that matched on a single point.
func TestCoverageBonusPromotesBroaderMatch(t *testing.T) {
	hits := []store.Hit{
		{MCP: model.MCP{ID: "single"}, Score: 0.5, MatchCount: 1},
		{MCP: model.MCP{ID: "broad"}, Score: 0.5, MatchCount: 10},
	}
	s := &Service{CoverageWeight: 0.6}
	out := s.finalize(hits)
	if out[0].MCP.ID != "broad" {
		t.Fatalf("broader match should be promoted, got %q", out[0].MCP.ID)
	}
}

// TestConfidenceIsAbsolute checks #4: Confidence reflects the pre-blend relevance
// magnitude (here the post-rerank Score), not the rank-relative final Score.
func TestConfidenceIsAbsolute(t *testing.T) {
	hits := []store.Hit{
		{MCP: model.MCP{ID: "a"}, Score: 0.8},
		{MCP: model.MCP{ID: "b"}, Score: 0.4},
	}
	s := &Service{} // no priors
	out := s.finalize(hits)
	if out[0].MCP.ID != "a" {
		t.Fatalf("expected a first, got %q", out[0].MCP.ID)
	}
	if got := out[0].Confidence; got < 0.79 || got > 0.81 {
		t.Fatalf("confidence should be the absolute pre-blend score ~0.8, got %v", got)
	}
	// The final Score is rank-relative (min-max), so the top is normalized to 1.0
	// — it must differ from the absolute confidence.
	if out[0].Score <= out[0].Confidence {
		t.Fatalf("final score (%v) should exceed absolute confidence (%v) for the top hit", out[0].Score, out[0].Confidence)
	}
}

func TestMinConfidenceFiltersWeakMatches(t *testing.T) {
	st := &fakeStore{hits: []store.Hit{
		{MCP: model.MCP{ID: "strong"}, Score: 0.9, MatchCount: 1},
		{MCP: model.MCP{ID: "ok"}, Score: 0.5, MatchCount: 1},
		{MCP: model.MCP{ID: "weak"}, Score: 0.1, MatchCount: 1},
	}}
	s := &Service{Embedder: fakeEmbedder{}, Store: st}
	out, err := s.Search(context.Background(), "q", 5, 0.4, store.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("min_score 0.4 should keep 2 of 3, got %d", len(out))
	}
	for _, h := range out {
		if h.Confidence < 0.4 {
			t.Fatalf("result %q below cutoff leaked through (confidence %v)", h.MCP.ID, h.Confidence)
		}
	}
}

func TestClampWeightsScalesDownAndFloors(t *testing.T) {
	wT, wU, wC := clampWeights(0.6, 0.6, 0.6) // sum 1.8 > 1
	if sum := wT + wU + wC; sum > 1.0001 {
		t.Fatalf("weights should sum to <= 1, got %v", sum)
	}
	wT, wU, wC = clampWeights(-1, 2, 0.5)
	if wT != 0 {
		t.Fatalf("negative weight should floor to 0, got %v", wT)
	}
	if wU+wT+wC > 1.0001 {
		t.Fatalf("clamped weights should still sum to <= 1")
	}
}

// --- fakes ---

type fakeEmbedder struct{}

func (fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{1, 0, 0}
	}
	return out, nil
}
func (fakeEmbedder) Dim() int     { return 3 }
func (fakeEmbedder) Name() string { return "fake" }

type fakeStore struct{ hits []store.Hit }

func (f *fakeStore) Search(_ context.Context, _ store.SearchQuery) ([]store.Hit, error) {
	return f.hits, nil
}
func (f *fakeStore) EnsureCollection(context.Context, int) error  { return nil }
func (f *fakeStore) Upsert(context.Context, []store.Record) error { return nil }
func (f *fakeStore) DeleteByMCPs(context.Context, []string) error { return nil }
func (f *fakeStore) Get(context.Context, string) (*model.MCP, error) {
	return nil, nil
}
func (f *fakeStore) Categories(context.Context) ([]string, error) { return nil, nil }
