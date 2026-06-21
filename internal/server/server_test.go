package server

import (
	"context"
	"testing"

	"github.com/vanducvt0305/zeus/internal/model"
	"github.com/vanducvt0305/zeus/internal/search"
	"github.com/vanducvt0305/zeus/internal/store"
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
