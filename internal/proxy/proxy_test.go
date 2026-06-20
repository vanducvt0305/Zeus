package proxy

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/vanducvt0305/zeus/internal/model"
)

func TestTextContentJoins(t *testing.T) {
	got := textContent([]mcp.Content{
		&mcp.TextContent{Text: "a"},
		&mcp.TextContent{Text: "b"},
	})
	if got != "a\nb" {
		t.Fatalf("textContent = %q, want %q", got, "a\nb")
	}
}

func TestCallNoRemoteEndpoint(t *testing.T) {
	p := New(nil, false, time.Second)
	// Package-only server: nothing remote to call.
	m := model.MCP{ID: "x", Packages: []model.Package{{RegistryType: "npm", Identifier: "foo"}}}
	_, err := p.Call(context.Background(), m, "do", nil)
	if err == nil || !strings.Contains(err.Error(), "no remote") {
		t.Fatalf("expected 'no remote' error, got %v", err)
	}
}
