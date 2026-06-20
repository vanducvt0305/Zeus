// Package store persists indexed MCP records as vectors plus metadata and
// answers nearest-neighbour queries. The Store interface keeps the rest of the
// system independent of Qdrant, so an alternative backend can be added later.
package store

import (
	"context"

	"github.com/vanducvt0305/zeus/internal/model"
	"github.com/vanducvt0305/zeus/internal/sparse"
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

// Record is a single point to upsert: the dense and sparse vectors for a piece
// of an MCP, plus the MCP it belongs to.
type Record struct {
	Kind     Kind          // server-, tool- or query-level
	ToolName string        // set when Kind == KindTool
	Query    string        // set when Kind == KindQuery
	Vector   []float32     // dense embedding, len == Embedder.Dim()
	Sparse   sparse.Vector // sparse keyword vector (may be empty)
	MCP      model.MCP     // full record, stored as payload for retrieval
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

// SearchQuery is one search request. When Sparse is non-empty, retrieval runs
// hybrid (dense + sparse) with Reciprocal Rank Fusion; otherwise it is
// dense-only.
type SearchQuery struct {
	Dense  []float32     // dense query embedding
	Sparse sparse.Vector // sparse query vector; empty => dense-only
	TopK   int           // number of distinct MCPs to return
	Filter Filter
}

// Store is the persistence backend for indexed MCPs.
type Store interface {
	// EnsureCollection creates the collection (named dense + sparse vectors) for
	// the given dense dimension if it does not already exist. Safe to repeat.
	EnsureCollection(ctx context.Context, dim int) error
	// Upsert inserts or replaces the given records.
	Upsert(ctx context.Context, records []Record) error
	// Search returns up to q.TopK distinct MCPs ranked by relevance.
	Search(ctx context.Context, q SearchQuery) ([]Hit, error)
	// DeleteByMCPs removes every point belonging to the given MCP ids. Used to
	// prune stale/orphan points (e.g. tools removed since the last index).
	DeleteByMCPs(ctx context.Context, ids []string) error
	// Get returns the MCP with the given id, or (nil, nil) if not found.
	Get(ctx context.Context, id string) (*model.MCP, error)
	// Categories returns the distinct categories present in the store.
	Categories(ctx context.Context) ([]string, error)
}
