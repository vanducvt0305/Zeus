package source

import (
	"strings"
	"testing"
)

func TestFilterTopicsDropsGeneric(t *testing.T) {
	got := filterTopics([]string{"mcp", "Database", "model-context-protocol", "search", "AI"})
	want := []string{"Database", "search"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("filterTopics = %v, want %v", got, want)
	}
}

func TestMergeCategoriesDedups(t *testing.T) {
	got := mergeCategories([]string{"web-search", "research"}, []string{"research", "", "tools"})
	want := []string{"web-search", "research", "tools"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("mergeCategories = %v, want %v", got, want)
	}
}

func TestServerJSONToMCP(t *testing.T) {
	var sj serverJSON
	sj.Name = "io.github.acme/search"
	sj.Description = "Search things."
	sj.Remotes = append(sj.Remotes, struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	}{Type: "streamable-http", URL: "https://acme.example/mcp"})

	m := sj.toMCP("github")
	if m.ID != "io.github.acme/search" || m.Source != "github" {
		t.Fatalf("unexpected id/source: %+v", m)
	}
	if len(m.Transports) != 1 || m.Transports[0].URL != "https://acme.example/mcp" {
		t.Fatalf("transport not mapped: %+v", m.Transports)
	}
}
