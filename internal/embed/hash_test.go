package embed

import (
	"context"
	"math"
	"testing"
)

func TestHashEmbedderDeterministicAndNormalized(t *testing.T) {
	e := NewHash(128)
	ctx := context.Background()

	vecs, err := e.Embed(ctx, []string{"search the web", "search the web"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 2 {
		t.Fatalf("want 2 vectors, got %d", len(vecs))
	}
	if len(vecs[0]) != 128 {
		t.Fatalf("want dim 128, got %d", len(vecs[0]))
	}

	// Same input must yield the same vector.
	for i := range vecs[0] {
		if vecs[0][i] != vecs[1][i] {
			t.Fatalf("embedding not deterministic at index %d", i)
		}
	}

	// Vector must be L2-normalized.
	var sum float64
	for _, v := range vecs[0] {
		sum += float64(v) * float64(v)
	}
	if math.Abs(sum-1) > 1e-5 {
		t.Fatalf("want unit norm, got %v", sum)
	}
}

func TestHashEmbedderEmptyInput(t *testing.T) {
	e := NewHash(64)
	vecs, err := e.Embed(context.Background(), []string{""})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	// An empty string produces the zero vector (no tokens to hash).
	for _, v := range vecs[0] {
		if v != 0 {
			t.Fatalf("want zero vector for empty input")
		}
	}
}
