// Package proxy turns Zeus from a directory into a switchboard: instead of the
// agent having to connect to a discovered MCP itself, it asks Zeus to call a
// tool on that MCP, and Zeus connects to the target and forwards the call. The
// agent then needs only one connection — to Zeus.
//
// Only remote transports (streamable-http/sse) are proxied; Zeus never installs
// or runs package-based (stdio) servers. Connections go through the same
// SSRF-guarded, credential-aware path as tool extraction.
package proxy

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/vanducvt0305/zeus/internal/extract"
	"github.com/vanducvt0305/zeus/internal/model"
)

// Result is the outcome of a forwarded tool call.
type Result struct {
	Content    string `json:"content,omitempty"`    // text content, concatenated
	Structured any    `json:"structured,omitempty"` // structured content, if any
	IsError    bool   `json:"is_error,omitempty"`   // tool-level error from the target
}

// Proxy forwards tool calls to remote MCP servers.
type Proxy struct {
	creds        *extract.Credentials
	allowPrivate bool
	timeout      time.Duration
}

// New builds a Proxy. timeout bounds each connect+call attempt.
func New(creds *extract.Credentials, allowPrivate bool, timeout time.Duration) *Proxy {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Proxy{creds: creds, allowPrivate: allowPrivate, timeout: timeout}
}

// Call connects to one of m's remote transports and invokes tool with args.
func (p *Proxy) Call(ctx context.Context, m model.MCP, tool string, args map[string]any) (Result, error) {
	var remotes int
	var lastErr error
	for _, t := range m.Transports {
		hc := extract.GuardedHTTPClient(p.allowPrivate, p.creds.Headers(m.ID, t.URL))
		transport := extract.ClientTransport(t, hc)
		if transport == nil {
			continue // non-remote / unsupported transport
		}
		remotes++
		res, err := p.call(ctx, transport, tool, args)
		if err != nil {
			lastErr = err
			continue
		}
		return res, nil
	}
	if remotes == 0 {
		return Result{}, fmt.Errorf("MCP %q has no remote (http/sse) endpoint to call; it must be installed locally", m.ID)
	}
	return Result{}, fmt.Errorf("calling %q on %q: %w", tool, m.ID, lastErr)
}

func (p *Proxy) call(ctx context.Context, transport mcp.Transport, tool string, args map[string]any) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	client := mcp.NewClient(&mcp.Implementation{Name: "zeus-proxy", Version: "0.1.0"}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return Result{}, fmt.Errorf("connect: %w", err)
	}
	defer session.Close()

	out, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		return Result{}, fmt.Errorf("tools/call: %w", err)
	}
	return Result{
		Content:    textContent(out.Content),
		Structured: out.StructuredContent,
		IsError:    out.IsError,
	}, nil
}

// textContent concatenates the text parts of a tool result.
func textContent(content []mcp.Content) string {
	var b strings.Builder
	for _, c := range content {
		if tc, ok := c.(*mcp.TextContent); ok {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}
