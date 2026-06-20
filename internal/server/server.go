// Package server exposes the MCP discovery service: the tools that agents call
// to find other MCP servers by describing, in natural language, what they need
// to do.
package server

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/vanducvt0305/zeus/internal/model"
	"github.com/vanducvt0305/zeus/internal/search"
	"github.com/vanducvt0305/zeus/internal/store"
)

// New builds the MCP discovery server, registering its tools.
func New(name, version string, svc *search.Service) *mcp.Server {
	s := &service{svc: svc}
	srv := mcp.NewServer(&mcp.Implementation{Name: name, Version: version}, nil)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "search_mcp",
		Description: "Find MCP servers that can perform a described task. " +
			"Describe the capability you need in natural language (e.g. " +
			"\"search product data\", \"send email\", \"query a Postgres database\") " +
			"and get back the most relevant MCP servers, ranked by semantic similarity.",
	}, s.searchMCP)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_mcp_details",
		Description: "Get the full details (description, connection info, tools, categories) of a single MCP server by its id.",
	}, s.getMCPDetails)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_categories",
		Description: "List the distinct categories present in the indexed MCP catalog, useful as filters for search_mcp.",
	}, s.listCategories)

	return srv
}

type service struct {
	svc *search.Service
}

// ---- search_mcp ----

type SearchInput struct {
	Query      string   `json:"query" jsonschema:"natural-language description of the capability the agent needs"`
	TopK       int      `json:"top_k,omitempty" jsonschema:"maximum number of MCP servers to return (default 5)"`
	Categories []string `json:"categories,omitempty" jsonschema:"optional category filter; results match at least one of these"`
	Source     string   `json:"source,omitempty" jsonschema:"optional source filter, e.g. \"registry\""`
}

type SearchResult struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description"`
	Score       float32  `json:"score"`
	MatchedTool string   `json:"matched_tool,omitempty"`
	Categories  []string `json:"categories,omitempty"`
	Repository  string   `json:"repository,omitempty"`
	Source      string   `json:"source"`
}

type SearchOutput struct {
	Results []SearchResult `json:"results"`
}

func (s *service) searchMCP(ctx context.Context, _ *mcp.CallToolRequest, in SearchInput) (*mcp.CallToolResult, SearchOutput, error) {
	if in.Query == "" {
		return nil, SearchOutput{}, fmt.Errorf("query must not be empty")
	}
	topK := in.TopK
	if topK <= 0 {
		topK = 5
	}

	hits, err := s.svc.Search(ctx, in.Query, topK, store.Filter{
		Categories: in.Categories,
		Source:     in.Source,
	})
	if err != nil {
		return nil, SearchOutput{}, err
	}

	out := SearchOutput{Results: make([]SearchResult, 0, len(hits))}
	for _, h := range hits {
		out.Results = append(out.Results, SearchResult{
			ID:          h.MCP.ID,
			Name:        h.MCP.Name,
			Title:       h.MCP.Title,
			Description: h.MCP.Description,
			Score:       h.Score,
			MatchedTool: h.ToolName,
			Categories:  h.MCP.AllCategories(),
			Repository:  h.MCP.Repository,
			Source:      h.MCP.Source,
		})
	}
	return nil, out, nil
}

// ---- get_mcp_details ----

type DetailsInput struct {
	ID string `json:"id" jsonschema:"the id of the MCP server (as returned by search_mcp)"`
}

type DetailsOutput struct {
	Found bool       `json:"found"`
	MCP   *model.MCP `json:"mcp,omitempty"`
}

func (s *service) getMCPDetails(ctx context.Context, _ *mcp.CallToolRequest, in DetailsInput) (*mcp.CallToolResult, DetailsOutput, error) {
	if in.ID == "" {
		return nil, DetailsOutput{}, fmt.Errorf("id must not be empty")
	}
	m, err := s.svc.Store.Get(ctx, in.ID)
	if err != nil {
		return nil, DetailsOutput{}, err
	}
	if m == nil {
		return nil, DetailsOutput{Found: false}, nil
	}
	return nil, DetailsOutput{Found: true, MCP: m}, nil
}

// ---- list_categories ----

type CategoriesInput struct{}

type CategoriesOutput struct {
	Categories []string `json:"categories"`
}

func (s *service) listCategories(ctx context.Context, _ *mcp.CallToolRequest, _ CategoriesInput) (*mcp.CallToolResult, CategoriesOutput, error) {
	cats, err := s.svc.Store.Categories(ctx)
	if err != nil {
		return nil, CategoriesOutput{}, err
	}
	return nil, CategoriesOutput{Categories: cats}, nil
}
