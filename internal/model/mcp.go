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
}

// DisplayName returns the human-friendly name, preferring Title.
func (m MCP) DisplayName() string {
	if strings.TrimSpace(m.Title) != "" {
		return m.Title
	}
	return m.Name
}

// EmbeddingText builds the text used to compute the server-level embedding.
// It deliberately front-loads the most discriminative fields (name, title,
// description) and appends categories and tool names for extra recall.
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
	write(m.Description)
	if len(m.Categories) > 0 {
		write("Categories: " + strings.Join(m.Categories, ", "))
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
