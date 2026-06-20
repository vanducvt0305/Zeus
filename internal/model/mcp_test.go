package model

import (
	"strings"
	"testing"
)

func TestEmbeddingTextPrefersEnrichment(t *testing.T) {
	m := MCP{
		Name:        "exa-web-search",
		Title:       "Exa",
		Description: "Neural web search engine.",
		Categories:  []string{"web-search"},
		Enrichment: Enrichment{
			Summary:    "Search the public web and read pages.",
			Tasks:      []string{"look up information online"},
			Synonyms:   []string{"google", "browse"},
			Categories: []string{"research"},
		},
	}
	got := m.EmbeddingText()

	// The normalized summary should be used in place of the raw description.
	if !strings.Contains(got, "Search the public web") {
		t.Errorf("embedding text missing enrichment summary:\n%s", got)
	}
	if strings.Contains(got, "Neural web search engine") {
		t.Errorf("embedding text should prefer summary over raw description:\n%s", got)
	}
	// Task language is what aligns docs with agent queries.
	if !strings.Contains(got, "look up information online") {
		t.Errorf("embedding text missing tasks:\n%s", got)
	}
}

func TestAllCategoriesMergesAndDedups(t *testing.T) {
	m := MCP{
		Categories: []string{"web-search", "research"},
		Enrichment: Enrichment{Categories: []string{"research", "tools"}},
	}
	got := m.AllCategories()
	want := []string{"web-search", "research", "tools"}
	if len(got) != len(want) {
		t.Fatalf("AllCategories = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("AllCategories = %v, want %v", got, want)
		}
	}
}
