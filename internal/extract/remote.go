package extract

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/vanducvt0305/zeus/internal/model"
)

// Remote extracts tools by connecting to a server's remote transports
// (streamable-http or sse) as an MCP client and calling tools/list. Each
// connection attempt is bounded by Timeout. When credentials are configured,
// the matching auth headers are attached so endpoints that gate tools/list
// behind auth can be reached.
type Remote struct {
	timeout time.Duration
	creds   *Credentials
	// Anonymous client reused when no credentials apply. No client-level
	// timeout: the per-attempt context deadline bounds the work, and a client
	// timeout would break the transport's SSE stream.
	httpClient *http.Client
}

// NewRemote builds a Remote extractor with the given per-attempt timeout and no
// credentials.
func NewRemote(timeout time.Duration) *Remote {
	return NewRemoteWithAuth(timeout, nil)
}

// NewRemoteWithAuth builds a Remote extractor that attaches credentials when
// they match the server being probed.
func NewRemoteWithAuth(timeout time.Duration, creds *Credentials) *Remote {
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	return &Remote{
		timeout:    timeout,
		creds:      creds,
		httpClient: &http.Client{},
	}
}

// httpClientFor returns a client that injects the given auth headers, or the
// shared anonymous client when there are none.
func (r *Remote) httpClientFor(headers map[string]string) *http.Client {
	if len(headers) == 0 {
		return r.httpClient
	}
	return &http.Client{Transport: headerRoundTripper{base: http.DefaultTransport, headers: headers}}
}

func (r *Remote) Name() string { return "remote" }

func (r *Remote) Extract(ctx context.Context, m model.MCP) (model.MCP, error) {
	// Respect tools a source already provided (e.g. fixtures).
	if len(m.Tools) > 0 {
		return m, nil
	}

	var lastErr error
	tried := false
	for _, t := range m.Transports {
		hc := r.httpClientFor(r.creds.Headers(m.ID, t.URL))
		transport := clientTransport(t, hc)
		if transport == nil {
			continue // unsupported / non-remote transport type
		}
		tried = true
		tools, err := r.listTools(ctx, transport)
		if err != nil {
			lastErr = err
			continue
		}
		if len(tools) > 0 {
			m.Tools = tools
			return m, nil
		}
	}
	if !tried {
		return m, nil // nothing connectable; not an error
	}
	if lastErr != nil {
		return m, fmt.Errorf("extract %q: %w", m.ID, lastErr)
	}
	return m, nil
}

// clientTransport maps a registry remote to an MCP client transport. Returns
// nil for transport types we don't connect to.
func clientTransport(t model.Transport, hc *http.Client) mcp.Transport {
	if strings.TrimSpace(t.URL) == "" {
		return nil
	}
	switch strings.ToLower(t.Type) {
	case "streamable-http", "streamable", "http":
		return &mcp.StreamableClientTransport{Endpoint: t.URL, HTTPClient: hc}
	case "sse":
		return &mcp.SSEClientTransport{Endpoint: t.URL, HTTPClient: hc}
	default:
		return nil
	}
}

func (r *Remote) listTools(ctx context.Context, transport mcp.Transport) ([]model.Tool, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	client := mcp.NewClient(&mcp.Implementation{Name: "zeus-extractor", Version: "0.1.0"}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer session.Close()

	var tools []model.Tool
	cursor := ""
	for {
		res, err := session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, fmt.Errorf("tools/list: %w", err)
		}
		for _, t := range res.Tools {
			tools = append(tools, model.Tool{Name: t.Name, Description: t.Description})
		}
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	return tools, nil
}
