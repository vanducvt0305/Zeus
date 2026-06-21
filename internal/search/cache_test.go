package search

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vanducvt0305/zeus/internal/model"
	"github.com/vanducvt0305/zeus/internal/store"
)

func TestResultCacheStoresCopies(t *testing.T) {
	c := newResultCache(time.Minute, 4)
	orig := []store.Hit{{MCP: model.MCP{ID: "a"}}, {MCP: model.MCP{ID: "b"}}}
	c.put("k", orig)

	got, ok := c.get("k")
	if !ok || len(got) != 2 {
		t.Fatalf("expected hit of 2, got ok=%v len=%d", ok, len(got))
	}
	// Mutating the returned slice must not corrupt the cached entry.
	got[0] = store.Hit{MCP: model.MCP{ID: "mutated"}}
	again, _ := c.get("k")
	if again[0].MCP.ID != "a" {
		t.Fatalf("cache entry was mutated through a returned slice: %q", again[0].MCP.ID)
	}
}

func TestResultCacheTTLExpiry(t *testing.T) {
	c := newResultCache(5*time.Millisecond, 4)
	c.put("k", []store.Hit{{MCP: model.MCP{ID: "a"}}})
	if _, ok := c.get("k"); !ok {
		t.Fatal("expected fresh hit")
	}
	time.Sleep(15 * time.Millisecond)
	if _, ok := c.get("k"); ok {
		t.Fatal("expected entry to expire")
	}
}

func TestResultCacheLRUEviction(t *testing.T) {
	c := newResultCache(time.Minute, 2)
	c.put("a", []store.Hit{{MCP: model.MCP{ID: "a"}}})
	c.put("b", []store.Hit{{MCP: model.MCP{ID: "b"}}})
	_, _ = c.get("a")                                  // touch a so b is the LRU
	c.put("c", []store.Hit{{MCP: model.MCP{ID: "c"}}}) // evicts b
	if _, ok := c.get("b"); ok {
		t.Fatal("b should have been evicted as least-recently-used")
	}
	if _, ok := c.get("a"); !ok {
		t.Fatal("a should still be cached")
	}
	if _, ok := c.get("c"); !ok {
		t.Fatal("c should be cached")
	}
}

func TestNilCacheIsNoop(t *testing.T) {
	var c *resultCache // disabled
	c.put("k", []store.Hit{{MCP: model.MCP{ID: "a"}}})
	if _, ok := c.get("k"); ok {
		t.Fatal("nil cache must never report a hit")
	}
}

func TestCacheKeyVariesAndStable(t *testing.T) {
	base := cacheKey("q", 5, 0, store.Filter{})
	if base != cacheKey("q", 5, 0, store.Filter{}) {
		t.Fatal("identical inputs must produce identical keys")
	}
	if base == cacheKey("q", 10, 0, store.Filter{}) {
		t.Fatal("top_k must affect the key")
	}
	if base == cacheKey("q", 5, 0.3, store.Filter{}) {
		t.Fatal("min_confidence must affect the key")
	}
	// Category order must not matter.
	a := cacheKey("q", 5, 0, store.Filter{Categories: []string{"x", "y"}})
	b := cacheKey("q", 5, 0, store.Filter{Categories: []string{"y", "x"}})
	if a != b {
		t.Fatal("category order should not change the key")
	}
}

func TestSearchShortCircuitsOnCacheHit(t *testing.T) {
	emb := &countingEmbedder{}
	s := &Service{Embedder: emb, Store: &fakeStore{hits: []store.Hit{
		{MCP: model.MCP{ID: "a"}, Score: 0.9},
	}}}
	s.EnableCache(time.Minute, 16)

	for i := 0; i < 3; i++ {
		if _, err := s.Search(context.Background(), "same query", 5, 0, store.Filter{}); err != nil {
			t.Fatal(err)
		}
	}
	if n := emb.calls.Load(); n != 1 {
		t.Fatalf("repeated identical queries should embed once, embedded %d times", n)
	}
	// A different query is a miss and re-embeds.
	if _, err := s.Search(context.Background(), "other query", 5, 0, store.Filter{}); err != nil {
		t.Fatal(err)
	}
	if n := emb.calls.Load(); n != 2 {
		t.Fatalf("a new query should embed again, total embeds = %d", n)
	}
}

type countingEmbedder struct{ calls atomic.Int64 }

func (e *countingEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	e.calls.Add(1)
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{1, 0, 0}
	}
	return out, nil
}
func (e *countingEmbedder) Dim() int     { return 3 }
func (e *countingEmbedder) Name() string { return "counting" }
