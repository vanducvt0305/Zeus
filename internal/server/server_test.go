package server

import (
	"context"
	"errors"
	"testing"

	"github.com/vanducvt0305/zeus/internal/model"
	"github.com/vanducvt0305/zeus/internal/proxy"
	"github.com/vanducvt0305/zeus/internal/search"
	"github.com/vanducvt0305/zeus/internal/store"
	"github.com/vanducvt0305/zeus/internal/usage"
)

func newTestService(hits []store.Hit) *service {
	return &service{svc: &search.Service{
		Embedder: fakeEmbedder{},
		Store:    &fakeStore{hits: hits},
	}}
}

func TestSearchMCPSurfacesConfidence(t *testing.T) {
	s := newTestService([]store.Hit{
		{MCP: model.MCP{ID: "io.github.acme/search", Name: "acme-search"}, Score: 0.9, MatchCount: 1},
	})
	_, out, err := s.searchMCP(context.Background(), nil, SearchInput{Query: "search the web"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(out.Results))
	}
	r := out.Results[0]
	if r.ID != "io.github.acme/search" {
		t.Fatalf("unexpected id %q", r.ID)
	}
	if r.Confidence < 0.89 || r.Confidence > 0.91 {
		t.Fatalf("confidence should pass through ~0.9, got %v", r.Confidence)
	}
}

func TestSearchMCPMinScoreDropsWeak(t *testing.T) {
	s := newTestService([]store.Hit{
		{MCP: model.MCP{ID: "strong"}, Score: 0.9, MatchCount: 1},
		{MCP: model.MCP{ID: "weak"}, Score: 0.1, MatchCount: 1},
	})
	_, out, err := s.searchMCP(context.Background(), nil, SearchInput{Query: "q", MinScore: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 1 || out.Results[0].ID != "strong" {
		t.Fatalf("min_score should keep only the strong match, got %+v", out.Results)
	}
}

func TestSearchMCPRejectsEmptyQuery(t *testing.T) {
	s := newTestService(nil)
	if _, _, err := s.searchMCP(context.Background(), nil, SearchInput{}); err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestGetDetailsExactHit(t *testing.T) {
	s := &service{svc: &search.Service{Embedder: fakeEmbedder{}, Store: &fakeStore{
		get: map[string]*model.MCP{"io.github.acme/search": {ID: "io.github.acme/search"}},
	}}}
	_, out, err := s.getMCPDetails(context.Background(), nil, DetailsInput{ID: "io.github.acme/search"})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Found || out.MCP == nil {
		t.Fatalf("expected exact hit, got %+v", out)
	}
}

func TestGetDetailsSuggestsOnMiss(t *testing.T) {
	// Exact Get misses; search surfaces near matches → found=false + suggestions.
	s := &service{svc: &search.Service{Embedder: fakeEmbedder{}, Store: &fakeStore{
		hits: []store.Hit{
			{MCP: model.MCP{ID: "io.github.acme/search"}, Score: 0.6},
			{MCP: model.MCP{ID: "io.github.acme/websearch"}, Score: 0.5},
		},
	}}}
	_, out, err := s.getMCPDetails(context.Background(), nil, DetailsInput{ID: "acme/serch"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Found {
		t.Fatal("expected not found for a typo'd id")
	}
	if len(out.Suggestions) == 0 || out.Suggestions[0] != "io.github.acme/search" {
		t.Fatalf("expected suggestions led by the closest id, got %v", out.Suggestions)
	}
}

func TestResolveAutoFixesCaseOnlyMismatch(t *testing.T) {
	// Exact Get is case-sensitive and misses, but search finds the same canonical
	// id in a different case → resolve it transparently.
	s := &service{svc: &search.Service{Embedder: fakeEmbedder{}, Store: &fakeStore{
		hits: []store.Hit{{MCP: model.MCP{ID: "io.github.Acme/Search"}, Score: 0.9}},
	}}}
	_, out, err := s.getMCPDetails(context.Background(), nil, DetailsInput{ID: "io.github.acme/search"})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Found || out.MCP == nil || out.MCP.ID != "io.github.Acme/Search" {
		t.Fatalf("case-only mismatch should resolve, got %+v", out)
	}
}

func TestCallMCPUnknownIDReturnsSuggestionsWithoutCalling(t *testing.T) {
	// proxy is nil: if the resolver wrongly fell through to a call, this panics.
	s := &service{svc: &search.Service{Embedder: fakeEmbedder{}, Store: &fakeStore{
		hits: []store.Hit{{MCP: model.MCP{ID: "io.github.acme/search"}, Score: 0.6}},
	}}}
	_, out, err := s.callMCP(context.Background(), nil, CallInput{MCPID: "acme/serch", Tool: "web_search"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Found {
		t.Fatal("unknown id must not be treated as found")
	}
	if len(out.Suggestions) == 0 {
		t.Fatal("expected suggestions for an unknown id")
	}
}

func TestCallOutcomeClassification(t *testing.T) {
	cases := []struct {
		name string
		res  proxy.Result
		err  error
		want usage.Outcome
	}{
		{"unreachable", proxy.Result{}, errors.New("connect: refused"), usage.OutcomeUnreachable},
		{"tool error is the caller's fault", proxy.Result{IsError: true}, nil, usage.OutcomeToolError},
		{"clean success", proxy.Result{Content: "ok"}, nil, usage.OutcomeSuccess},
		{"error wins over is_error", proxy.Result{IsError: true}, errors.New("boom"), usage.OutcomeUnreachable},
	}
	for _, c := range cases {
		if got := callOutcome(c.res, c.err); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
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

type fakeStore struct {
	hits []store.Hit
	get  map[string]*model.MCP // exact-id lookups for Get
}

func (f *fakeStore) Search(_ context.Context, _ store.SearchQuery) ([]store.Hit, error) {
	return f.hits, nil
}
func (f *fakeStore) EnsureCollection(context.Context, int) error  { return nil }
func (f *fakeStore) Upsert(context.Context, []store.Record) error { return nil }
func (f *fakeStore) DeleteByMCPs(context.Context, []string) error { return nil }
func (f *fakeStore) Get(_ context.Context, id string) (*model.MCP, error) {
	if m, ok := f.get[id]; ok {
		return m, nil
	}
	return nil, nil
}
func (f *fakeStore) Categories(context.Context) ([]string, error) { return nil, nil }
