package extract

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/vanducvt0305/zeus/internal/model"
)

func TestClientTransportMapping(t *testing.T) {
	hc := newRemoteHTTP()
	cases := []struct {
		transport model.Transport
		want      string // "streamable", "sse", or "" for nil
	}{
		{model.Transport{Type: "streamable-http", URL: "https://x.example/mcp"}, "streamable"},
		{model.Transport{Type: "sse", URL: "https://x.example/sse"}, "sse"},
		{model.Transport{Type: "stdio", URL: ""}, ""},          // not remote
		{model.Transport{Type: "streamable-http", URL: ""}, ""}, // no endpoint
		{model.Transport{Type: "weird", URL: "https://x"}, ""},  // unsupported
	}
	for _, c := range cases {
		got := clientTransport(c.transport, hc)
		switch c.want {
		case "":
			if got != nil {
				t.Errorf("%+v: want nil transport, got %T", c.transport, got)
			}
		case "streamable":
			if _, ok := got.(*mcp.StreamableClientTransport); !ok {
				t.Errorf("%+v: want StreamableClientTransport, got %T", c.transport, got)
			}
		case "sse":
			if _, ok := got.(*mcp.SSEClientTransport); !ok {
				t.Errorf("%+v: want SSEClientTransport, got %T", c.transport, got)
			}
		}
	}
}

func TestExtractKeepsExistingTools(t *testing.T) {
	m := model.MCP{
		ID:    "x",
		Tools: []model.Tool{{Name: "already_here"}},
		Transports: []model.Transport{
			{Type: "streamable-http", URL: "https://unreachable.invalid/mcp"},
		},
	}
	// Must return immediately without attempting a network connection.
	out, err := NewRemote(time.Second).Extract(context.Background(), m)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(out.Tools) != 1 || out.Tools[0].Name != "already_here" {
		t.Fatalf("existing tools should be preserved, got %+v", out.Tools)
	}
}

func TestExtractNoRemotesIsNotError(t *testing.T) {
	m := model.MCP{ID: "y", Packages: []model.Package{{RegistryType: "npm", Identifier: "foo"}}}
	out, err := NewRemote(time.Second).Extract(context.Background(), m)
	if err != nil {
		t.Fatalf("expected no error for package-only server, got %v", err)
	}
	if len(out.Tools) != 0 {
		t.Fatalf("expected no tools, got %+v", out.Tools)
	}
}

// newRemoteHTTP exposes the same client the extractor uses, for the mapping test.
func newRemoteHTTP() *http.Client { return NewRemote(time.Second).httpClient }
