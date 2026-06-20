// Package extract enriches an MCP record with the real tool list of the server.
//
// The registry (and most catalogs) describe how to *connect to* an MCP server —
// its remote endpoints and installable packages — but not the tools it exposes.
// Tools are the most query-relevant signal an agent searches for, so we recover
// them by actually connecting to the server and calling tools/list.
//
// Safety: we only connect to remote HTTP(S) endpoints. We deliberately do NOT
// install or execute package-based (stdio) servers, since that would run
// untrusted third-party code.
package extract

import (
	"context"

	"github.com/vanducvt0305/zeus/internal/model"
)

// Extractor populates an MCP's tool list.
type Extractor interface {
	// Extract returns a copy of m with m.Tools populated when possible. It is
	// best-effort: failures (unreachable server, auth required, no remotes)
	// return the record unchanged with an error, never a fatal stop.
	Extract(ctx context.Context, m model.MCP) (model.MCP, error)
	// Name identifies the extractor, for logging.
	Name() string
}

// Noop leaves records unchanged. Used when tool extraction is disabled, or for
// sources (like fixtures) that already carry tools.
type Noop struct{}

func (Noop) Name() string { return "noop" }

func (Noop) Extract(_ context.Context, m model.MCP) (model.MCP, error) {
	return m, nil
}
