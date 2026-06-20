package source

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/vanducvt0305/zeus/internal/model"
)

// DefaultRegistryURL is the official Model Context Protocol registry.
const DefaultRegistryURL = "https://registry.modelcontextprotocol.io"

// Registry fetches servers from a registry that speaks the official
// /v0/servers API and returns server.json documents.
//
// Note: the registry describes how to *connect to* a server (remotes,
// packages) but does not list each server's tools. Records from this source
// therefore carry an empty Tools list and are embedded at server granularity
// only. Tool-level enrichment is a job for other sources (e.g. connecting to a
// server and calling tools/list, or parsing a GitHub README).
type Registry struct {
	baseURL string
	client  *http.Client
}

// NewRegistry returns a Registry client. An empty baseURL uses the official
// registry.
func NewRegistry(baseURL string) *Registry {
	if baseURL == "" {
		baseURL = DefaultRegistryURL
	}
	return &Registry{
		baseURL: baseURL,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

func (r *Registry) Name() string { return "registry" }

// registryResponse mirrors the subset of GET /v0/servers we consume.
type registryResponse struct {
	Servers  []registryEntry `json:"servers"`
	Metadata struct {
		NextCursor string `json:"nextCursor"`
		Count      int    `json:"count"`
	} `json:"metadata"`
}

type registryEntry struct {
	Server serverJSON `json:"server"`
	Meta   struct {
		Official struct {
			Status    string `json:"status"`
			IsLatest  bool   `json:"isLatest"`
			UpdatedAt string `json:"updatedAt"`
		} `json:"io.modelcontextprotocol.registry/official"`
	} `json:"_meta"`
}

func (r *Registry) Fetch(ctx context.Context, limit int) ([]model.MCP, error) {
	const pageSize = 100
	var out []model.MCP
	cursor := ""

	for {
		page, next, err := r.fetchPage(ctx, cursor, pageSize)
		if err != nil {
			return nil, err
		}
		for _, e := range page.Servers {
			// The registry keeps every published version; index only the
			// latest of each server.
			if !e.Meta.Official.IsLatest {
				continue
			}
			out = append(out, mapServer(e))
			if limit > 0 && len(out) >= limit {
				return out, nil
			}
		}
		if next == "" {
			break
		}
		cursor = next
	}
	return out, nil
}

func (r *Registry) fetchPage(ctx context.Context, cursor string, pageSize int) (registryResponse, string, error) {
	u, err := url.Parse(r.baseURL + "/v0/servers")
	if err != nil {
		return registryResponse{}, "", err
	}
	q := u.Query()
	q.Set("limit", strconv.Itoa(pageSize))
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return registryResponse{}, "", err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return registryResponse{}, "", fmt.Errorf("fetching %s: %w", u, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if resp.StatusCode != http.StatusOK {
		return registryResponse{}, "", fmt.Errorf("registry returned %s", resp.Status)
	}
	var parsed registryResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return registryResponse{}, "", fmt.Errorf("decoding registry page: %w", err)
	}
	return parsed, parsed.Metadata.NextCursor, nil
}

func mapServer(e registryEntry) model.MCP {
	m := e.Server.toMCP("registry")
	m.UpdatedAt = e.Meta.Official.UpdatedAt
	return m
}
