package extract

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// Credentials resolves the HTTP headers to send when probing a server for its
// tools, so extraction can reach endpoints that gate tools/list behind auth.
//
// Resolution order for a given MCP id and endpoint URL:
//  1. an entry keyed by the exact MCP id
//  2. an entry keyed by the exact endpoint host
//  3. an entry keyed by a "*.suffix" host wildcard
//  4. the global bearer token, if set
//  5. nothing (anonymous)
type Credentials struct {
	entries map[string]map[string]string // matcher -> header name -> value
	token   string                       // global bearer fallback
}

// NewCredentials builds a provider from an in-memory map and optional global
// bearer token. Either may be empty.
func NewCredentials(token string, entries map[string]map[string]string) *Credentials {
	if entries == nil {
		entries = map[string]map[string]string{}
	}
	return &Credentials{entries: entries, token: token}
}

// LoadCredentials reads a JSON credentials file (matcher -> {header: value}).
// An empty path is allowed and yields a provider with only the global token.
func LoadCredentials(path, token string) (*Credentials, error) {
	entries := map[string]map[string]string{}
	if strings.TrimSpace(path) != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading credentials file: %w", err)
		}
		if err := json.Unmarshal(raw, &entries); err != nil {
			return nil, fmt.Errorf("decoding credentials file %s: %w", path, err)
		}
	}
	return NewCredentials(token, entries), nil
}

// Empty reports whether the provider would never attach any header.
func (c *Credentials) Empty() bool {
	return c == nil || (len(c.entries) == 0 && c.token == "")
}

// Headers returns the headers to attach for the given MCP id and endpoint URL,
// or nil if none apply.
func (c *Credentials) Headers(mcpID, rawURL string) map[string]string {
	if c == nil {
		return nil
	}
	if h, ok := c.entries[mcpID]; ok {
		return h
	}
	if host := hostOf(rawURL); host != "" {
		if h, ok := c.entries[host]; ok {
			return h
		}
		for matcher, h := range c.entries {
			if suffix, ok := strings.CutPrefix(matcher, "*."); ok {
				if host == suffix || strings.HasSuffix(host, "."+suffix) {
					return h
				}
			}
		}
	}
	if c.token != "" {
		return map[string]string{"Authorization": "Bearer " + c.token}
	}
	return nil
}

func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// headerRoundTripper injects fixed headers into every outgoing request.
type headerRoundTripper struct {
	base    http.RoundTripper
	headers map[string]string
}

func (h headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if len(h.headers) > 0 {
		req = req.Clone(req.Context())
		for k, v := range h.headers {
			req.Header.Set(k, v)
		}
	}
	base := h.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}
