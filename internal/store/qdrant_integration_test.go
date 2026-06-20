package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/vanducvt0305/zeus/internal/model"
	"github.com/vanducvt0305/zeus/internal/sparse"
)

// TestQdrantIntegration exercises the real store end-to-end. It is skipped when
// Qdrant isn't reachable, so `go test ./...` still passes without it. Point it
// at a running instance with QDRANT_HOST/QDRANT_PORT (defaults localhost:6334).
func TestQdrantIntegration(t *testing.T) {
	host := envOr("QDRANT_HOST", "localhost")
	port := 6334
	collection := fmt.Sprintf("zeus_it_%d", time.Now().UnixNano())

	q, err := NewQdrant(host, port, os.Getenv("QDRANT_API_KEY"), collection)
	if err != nil {
		t.Skipf("qdrant unavailable: %v", err)
	}
	defer q.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := q.EnsureCollection(ctx, 4); err != nil {
		t.Skipf("qdrant unavailable (EnsureCollection): %v", err)
	}
	defer q.client.DeleteCollection(context.Background(), collection)

	enc := sparse.Lexical{}
	alpha := model.MCP{ID: "alpha", Name: "alpha", Description: "send email to people", Categories: []string{"comms"}, Source: "test", Tools: []model.Tool{{Name: "send_email"}}}
	beta := model.MCP{ID: "beta", Name: "beta", Description: "query a database", Categories: []string{"db"}, Source: "test"}

	recs := []Record{
		{Kind: KindServer, MCP: alpha, Vector: []float32{1, 0, 0, 0}, Sparse: enc.Encode(alpha.Description)},
		{Kind: KindTool, ToolName: "send_email", MCP: alpha, Vector: []float32{0.9, 0.1, 0, 0}, Sparse: enc.Encode("send email")},
		{Kind: KindServer, MCP: beta, Vector: []float32{0, 1, 0, 0}, Sparse: enc.Encode(beta.Description)},
	}
	if err := q.Upsert(ctx, recs); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Dense search near alpha's vector should return alpha first.
	hits, err := q.Search(ctx, SearchQuery{Dense: []float32{1, 0, 0, 0}, TopK: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) == 0 || hits[0].MCP.ID != "alpha" {
		t.Fatalf("expected alpha first, got %+v", hits)
	}

	// Get returns the full record (with tools).
	got, err := q.Get(ctx, "alpha")
	if err != nil || got == nil {
		t.Fatalf("Get(alpha) = %v, %v", got, err)
	}
	if len(got.Tools) != 1 || got.Tools[0].Name != "send_email" {
		t.Fatalf("Get(alpha) tools = %+v", got.Tools)
	}

	// Categories via facet.
	cats, err := q.Categories(ctx)
	if err != nil {
		t.Fatalf("Categories: %v", err)
	}
	if !contains(cats, "comms") || !contains(cats, "db") {
		t.Fatalf("Categories = %v, want comms and db", cats)
	}

	// Filter by source.
	fhits, err := q.Search(ctx, SearchQuery{Dense: []float32{0, 1, 0, 0}, TopK: 5, Filter: Filter{Source: "test"}})
	if err != nil || len(fhits) == 0 {
		t.Fatalf("filtered Search returned nothing: %v", err)
	}

	// Delete alpha; it should disappear, beta should remain.
	if err := q.DeleteByMCPs(ctx, []string{"alpha"}); err != nil {
		t.Fatalf("DeleteByMCPs: %v", err)
	}
	if got, _ := q.Get(ctx, "alpha"); got != nil {
		t.Fatalf("alpha should be deleted, still present")
	}
	if got, _ := q.Get(ctx, "beta"); got == nil {
		t.Fatalf("beta should still exist")
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
