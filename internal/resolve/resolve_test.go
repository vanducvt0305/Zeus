package resolve

import (
	"strings"
	"testing"

	"github.com/vanducvt0305/zeus/internal/model"
)

func TestNormalizeRepoEquivalence(t *testing.T) {
	a := normalizeRepo("https://github.com/Acme/Search.git")
	b := normalizeRepo("git@github.com:acme/search")
	c := normalizeRepo("http://www.github.com/acme/search/")
	if a != "github.com/acme/search" {
		t.Fatalf("normalizeRepo = %q", a)
	}
	if a != b || a != c {
		t.Fatalf("expected equal keys, got %q / %q / %q", a, b, c)
	}
}

func TestDedupMergesAcrossSources(t *testing.T) {
	registry := model.MCP{
		ID:          "io.github.acme/search",
		Name:        "io.github.acme/search",
		Description: "Authoritative registry description.",
		Version:     "1.2.0",
		Repository:  "https://github.com/acme/search",
		Source:      "registry",
		Transports:  []model.Transport{{Type: "streamable-http", URL: "https://acme.example/mcp"}},
	}
	github := model.MCP{
		ID:         "io.github.acme/search",
		Name:       "io.github.acme/search", // same canonical name => same MCP
		Title:      "Search",                // registry lacked a title
		Repository: "https://github.com/Acme/Search.git",
		Source:     "github",
		Categories: []string{"web-search"},
		Tools:      []model.Tool{{Name: "web_search"}}, // tools came from the GitHub side
	}

	out := Dedup([]model.MCP{github, registry}) // intentionally github-first
	if len(out) != 1 {
		t.Fatalf("expected 1 merged record, got %d", len(out))
	}
	m := out[0]

	// Registry wins scalar identity/description despite github being first in.
	if m.Source != "registry" {
		t.Errorf("primary Source = %q, want registry", m.Source)
	}
	if !strings.Contains(m.Description, "Authoritative") {
		t.Errorf("description not from registry: %q", m.Description)
	}
	// Fields the registry lacked are filled from github.
	if m.Title != "Search" {
		t.Errorf("title = %q, want filled from github", m.Title)
	}
	// Provenance records both, in priority order.
	if strings.Join(m.Sources, ",") != "registry,github" {
		t.Errorf("Sources = %v, want [registry github]", m.Sources)
	}
	// List fields unioned.
	if len(m.Tools) != 1 || m.Tools[0].Name != "web_search" {
		t.Errorf("tools not merged: %+v", m.Tools)
	}
	if len(m.Transports) != 1 {
		t.Errorf("transports = %+v", m.Transports)
	}
}

func TestDedupKeepsDistinct(t *testing.T) {
	in := []model.MCP{
		{ID: "a", Name: "a", Repository: "https://github.com/x/a", Source: "github"},
		{ID: "b", Name: "b", Repository: "https://github.com/x/b", Source: "github"},
	}
	if out := Dedup(in); len(out) != 2 {
		t.Fatalf("expected 2 distinct records, got %d", len(out))
	}
}

func TestDedupDoesNotMergeMonorepoSiblings(t *testing.T) {
	// Distinct servers that share one monorepo must stay distinct.
	in := []model.MCP{
		{ID: "io.modelcontextprotocol/fetch", Name: "io.modelcontextprotocol/fetch", Repository: "https://github.com/modelcontextprotocol/servers", Source: "registry"},
		{ID: "io.modelcontextprotocol/filesystem", Name: "io.modelcontextprotocol/filesystem", Repository: "https://github.com/modelcontextprotocol/servers", Source: "registry"},
	}
	if out := Dedup(in); len(out) != 2 {
		t.Fatalf("monorepo siblings must not merge; got %d records", len(out))
	}
}

func TestDedupByNameWhenNoRepo(t *testing.T) {
	in := []model.MCP{
		{ID: "x", Name: "weather", Description: "", Source: "github"},
		{ID: "x", Name: "Weather", Description: "Forecasts.", Source: "registry"},
	}
	out := Dedup(in)
	if len(out) != 1 {
		t.Fatalf("expected merge by name, got %d", len(out))
	}
	if out[0].Description != "Forecasts." {
		t.Errorf("description = %q", out[0].Description)
	}
}
