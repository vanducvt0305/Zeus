package sparse

import (
	"math"
	"testing"
)

func TestLexicalEncodeUniqueAndNormalized(t *testing.T) {
	v := Lexical{}.Encode("send an email to a customer")
	if v.Empty() {
		t.Fatal("expected non-empty vector")
	}
	if len(v.Indices) != len(v.Values) {
		t.Fatalf("indices/values length mismatch: %d vs %d", len(v.Indices), len(v.Values))
	}
	// Indices must be unique (Qdrant requirement).
	seen := make(map[uint32]bool)
	for _, id := range v.Indices {
		if seen[id] {
			t.Fatalf("duplicate sparse index %d", id)
		}
		seen[id] = true
	}
	// Stopwords ("an", "to", "a") should be dropped, leaving send/email/customer.
	if len(v.Indices) != 3 {
		t.Fatalf("want 3 terms after stopword removal, got %d", len(v.Indices))
	}
	// L2 norm ~ 1.
	var sum float64
	for _, w := range v.Values {
		sum += float64(w) * float64(w)
	}
	if math.Abs(sum-1) > 1e-5 {
		t.Fatalf("want unit norm, got %v", sum)
	}
}

func TestLexicalSharedTermsOverlap(t *testing.T) {
	a := Lexical{}.Encode("send an email message")
	b := Lexical{}.Encode("email a customer")
	overlap := 0
	set := make(map[uint32]bool)
	for _, id := range a.Indices {
		set[id] = true
	}
	for _, id := range b.Indices {
		if set[id] {
			overlap++
		}
	}
	if overlap == 0 {
		t.Fatal("expected the shared term 'email' to produce overlapping indices")
	}
}

func TestLexicalEmptyForStopwordsOnly(t *testing.T) {
	if v := (Lexical{}).Encode("the and for with"); !v.Empty() {
		t.Fatalf("want empty vector for stopwords-only input, got %d terms", len(v.Indices))
	}
}
