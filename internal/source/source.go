// Package source fetches MCP server descriptions from external catalogs and
// normalizes them into model.MCP. The Source interface lets the indexer treat
// every catalog (the official registry, GitHub, aggregators) uniformly.
package source

import (
	"context"

	"github.com/vanducvt0305/zeus/internal/model"
)

// Source is a catalog of MCP servers.
type Source interface {
	// Name identifies the catalog, stored on each record as MCP.Source.
	Name() string
	// Fetch returns up to limit normalized MCP records. A limit <= 0 means
	// "fetch everything the source exposes".
	Fetch(ctx context.Context, limit int) ([]model.MCP, error)
}
