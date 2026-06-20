// Package model defines the normalized schema for an indexed MCP server.
//
// Different sources (the official MCP registry, GitHub, aggregators) describe
// MCP servers with different shapes. The indexer maps every source into this
// single MCP type before embedding and storing it, so the rest of the system
// only ever deals with one representation.
package model

import (
	"strings"
)

// Transport describes one way of connecting to an MCP server at runtime,
// e.g. a remote streamable-http endpoint or a stdio package.
type Transport struct {
	Type string `json:"type"`          // streamable-http, sse, stdio, ...
	URL  string `json:"url,omitempty"` // for remote transports
}

// Package describes an installable artifact (npm/pypi/oci/...) that runs the
// MCP server locally.
type Package struct {
	RegistryType string `json:"registryType,omitempty"` // npm, pypi, oci, nuget, ...
	Identifier   string `json:"identifier,omitempty"`   // package name / image
	Version      string `json:"version,omitempty"`
	Transport    string `json:"transport,omitempty"` // stdio, streamable-http, ...
}

// Tool is a single capability exposed by an MCP server. When a source provides
// the tool list, each tool is embedded individually so that tool-shaped agent
// queries ("search data", "send email") match at a fine granularity.
type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// MCP is the normalized record for one MCP server.
type MCP struct {
	ID          string      `json:"id"`          // stable identifier (the source name/slug)
	Name        string      `json:"name"`        // canonical name, e.g. "io.github.acme/search"
	Title       string      `json:"title,omitempty"`
	Description string      `json:"description"`
	Version     string      `json:"version,omitempty"`
	Repository  string      `json:"repository,omitempty"`
	Homepage    string      `json:"homepage,omitempty"`
	Categories  []string    `json:"categories,omitempty"`
	Transports  []Transport `json:"transports,omitempty"`
	Packages    []Package   `json:"packages,omitempty"`
	Tools       []Tool      `json:"tools,omitempty"`
	Source      string      `json:"source"`              // "registry", "github", ...
	UpdatedAt   string      `json:"updatedAt,omitempty"` // RFC3339 from the source, if known

	// Enrichment is the generated "capability card". It is empty until the
	// enrichment stage runs, and is what gives search its quality: it rewrites
	// a terse, marketing-flavored MCP into the task language agents actually
	// query in. See package enrich.
	Enrichment Enrichment `json:"enrichment,omitempty"`
}

// Enrichment is the normalized capability card produced for an MCP. Every field
// is optional; richer enrichers (LLM-backed) fill more of it than the offline
// heuristic one.
type Enrichment struct {
	// Summary is a normalized one-paragraph description of what the MCP does.
	Summary string `json:"summary,omitempty"`
	// Tasks are the "jobs to be done" phrased in agent-intent language,
	// e.g. "extract tables from a PDF", "cancel a hotel booking".
	Tasks []string `json:"tasks,omitempty"`
	// ExampleQueries are synthetic queries an agent might issue that this MCP
	// answers. They are indexed as their own vectors to bridge the gap between
	// how agents ask and how MCPs describe themselves.
	ExampleQueries []string `json:"exampleQueries,omitempty"`
	// Synonyms are alternative terms for the MCP's domain and capabilities.
	Synonyms []string `json:"synonyms,omitempty"`
	// Categories are derived domain categories (may extend MCP.Categories).
	Categories []string `json:"categories,omitempty"`
	// Enricher records which enricher produced this card, for debugging.
	Enricher string `json:"enricher,omitempty"`
}

// IsEmpty reports whether no enrichment has been applied.
func (e Enrichment) IsEmpty() bool {
	return e.Summary == "" && len(e.Tasks) == 0 && len(e.ExampleQueries) == 0 &&
		len(e.Synonyms) == 0 && len(e.Categories) == 0
}

// AllCategories merges source-provided and enrichment-derived categories,
// de-duplicated and stable-ordered.
func (m MCP) AllCategories() []string {
	seen := make(map[string]struct{})
	var out []string
	for _, c := range append(append([]string{}, m.Categories...), m.Enrichment.Categories...) {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	return out
}

// DisplayName returns the human-friendly name, preferring Title.
func (m MCP) DisplayName() string {
	if strings.TrimSpace(m.Title) != "" {
		return m.Title
	}
	return m.Name
}

// EmbeddingText builds the text used to compute the server-level embedding.
// It front-loads the most discriminative fields and, when enrichment is
// present, prefers the normalized summary and task list over the raw
// description — that is what aligns the document with agent-intent queries.
func (m MCP) EmbeddingText() string {
	var b strings.Builder
	write := func(s string) {
		if s = strings.TrimSpace(s); s != "" {
			b.WriteString(s)
			b.WriteString("\n")
		}
	}
	write(m.Title)
	write(m.Name)
	if s := strings.TrimSpace(m.Enrichment.Summary); s != "" {
		write(s)
	} else {
		write(m.Description)
	}
	if len(m.Enrichment.Tasks) > 0 {
		write("Can be used to: " + strings.Join(m.Enrichment.Tasks, "; "))
	}
	if len(m.Enrichment.Synonyms) > 0 {
		write("Related: " + strings.Join(m.Enrichment.Synonyms, ", "))
	}
	if cats := m.AllCategories(); len(cats) > 0 {
		write("Categories: " + strings.Join(cats, ", "))
	}
	if len(m.Tools) > 0 {
		names := make([]string, 0, len(m.Tools))
		for _, t := range m.Tools {
			names = append(names, t.Name)
		}
		write("Tools: " + strings.Join(names, ", "))
	}
	return strings.TrimSpace(b.String())
}

// ToolEmbeddingText builds the embedding text for a single tool, including
// minimal parent context so the tool stays grounded in its server.
func (m MCP) ToolEmbeddingText(t Tool) string {
	var b strings.Builder
	if s := strings.TrimSpace(t.Name); s != "" {
		b.WriteString(s)
		b.WriteString("\n")
	}
	if s := strings.TrimSpace(t.Description); s != "" {
		b.WriteString(s)
		b.WriteString("\n")
	}
	if s := strings.TrimSpace(m.DisplayName()); s != "" {
		b.WriteString("From MCP: " + s)
	}
	return strings.TrimSpace(b.String())
}
