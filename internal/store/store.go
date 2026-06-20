// Package store persists indexed MCP records as vectors plus metadata and
// answers nearest-neighbour queries. The Store interface keeps the rest of the
// system independent of Qdrant, so an alternative backend can be added later.
package store

import (
	"context"

	"github.com/vanducvt0305/zeus/internal/model"
)

// Kind distinguishes the two granularities at which an MCP is indexed.
type Kind string

const (
	// KindServer is one vector per MCP, built from name+description+categories.
	KindServer Kind = "server"
	// KindTool is one vector per tool, built from the tool's name+description.
	KindTool Kind = "tool"
	// KindQuery is one vector per synthetic example query from enrichment. These
	// points carry the agent-intent language directly, so they match real
	// queries closely; search collapses them back to their parent MCP.
	KindQuery Kind = "query"
)

// Record is a single point to upsert: a vector plus the MCP it belongs to.
type Record struct {
	Kind     Kind      // server-, tool- or query-level
	ToolName string    // set when Kind == KindTool
	Query    string    // set when Kind == KindQuery
	Vector   []float32 // embedding, len == Embedder.Dim()
	MCP      model.MCP // full record, stored as payload for retrieval
}

// discriminator is the per-MCP sub-key that makes a point id unique and stable
// across re-indexing, so updates overwrite rather than duplicate.
func (r Record) discriminator() string {
	switch r.Kind {
	case KindTool:
		return r.ToolName
	case KindQuery:
		return r.Query
	default:
		return ""
	}
}

// Filter narrows a search to a subset of records.
type Filter struct {
	Categories []string // match if the MCP has ANY of these categories
	Source     string   // match a specific source ("registry", "github", ...)
}

// Hit is a single search result, already deduplicated to one entry per MCP.
type Hit struct {
	MCP       model.MCP
	Score     float32
	MatchKind Kind   // whether the best match was server- or tool-level
	ToolName  string // the matched tool, when MatchKind == KindTool
}

// Store is the persistence backend for indexed MCPs.
type Store interface {
	// EnsureCollection creates the collection for the given vector dimension if
	// it does not already exist. It is safe to call repeatedly.
	EnsureCollection(ctx context.Context, dim int) error
	// Upsert inserts or replaces the given records.
	Upsert(ctx context.Context, records []Record) error
	// Search returns up to topK distinct MCPs ranked by similarity to vec.
	Search(ctx context.Context, vec []float32, topK int, filter Filter) ([]Hit, error)
	// Get returns the MCP with the given id, or (nil, nil) if not found.
	Get(ctx context.Context, id string) (*model.MCP, error)
	// Categories returns the distinct categories present in the store.
	Categories(ctx context.Context) ([]string, error)
}
