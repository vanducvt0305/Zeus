// Package server exposes the MCP discovery service: the tools that agents call
// to find other MCP servers by describing, in natural language, what they need
// to do.
package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/vanducvt0305/zeus/internal/model"
	"github.com/vanducvt0305/zeus/internal/proxy"
	"github.com/vanducvt0305/zeus/internal/search"
	"github.com/vanducvt0305/zeus/internal/store"
	"github.com/vanducvt0305/zeus/internal/usage"
)

// New builds the MCP discovery server, registering its tools. When prx is
// non-nil, the call_mcp tool is exposed so the server can forward calls to
// discovered MCPs (router/proxy mode).
func New(name, version string, svc *search.Service, prx *proxy.Proxy) *mcp.Server {
	s := &service{svc: svc, proxy: prx}
	srv := mcp.NewServer(&mcp.Implementation{Name: name, Version: version}, nil)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "search_mcp",
		Description: "Find MCP servers that can perform a described task. " +
			"Describe the capability you need in natural language (e.g. " +
			"\"search product data\", \"send email\", \"query a Postgres database\") " +
			"and get back the most relevant MCP servers, ranked by semantic similarity. " +
			"Each result has a 'confidence' (0..1) measuring how strongly it matches the " +
			"query on its own; use 'min_score' to drop weak matches so you can tell a " +
			"strong hit from the best of a poor field.",
	}, s.searchMCP)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_mcp_details",
		Description: "Get the full details (description, connection info, tools, categories) of a single MCP server by its id. " +
			"If the id isn't found exactly, the response has found=false and a 'suggestions' list of the closest ids — retry with one of those.",
	}, s.getMCPDetails)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_categories",
		Description: "List the distinct categories present in the indexed MCP catalog, useful as filters for search_mcp.",
	}, s.listCategories)

	if s.proxy != nil {
		mcp.AddTool(srv, &mcp.Tool{
			Name: "call_mcp",
			Description: "Call a tool on another MCP server that you found via search_mcp, without connecting to it yourself. " +
				"Give the mcp id, the tool name, and its arguments; Zeus connects to that server and forwards the call, returning its result. " +
				"Works for servers with a remote (http/sse) endpoint. " +
				"If the mcp id isn't found, the response has found=false and a 'suggestions' list of the closest ids — Zeus never guesses and forwards to a different server.",
		}, s.callMCP)
	}

	return srv
}

type service struct {
	svc   *search.Service
	proxy *proxy.Proxy
}

// ---- search_mcp ----

type SearchInput struct {
	Query      string   `json:"query" jsonschema:"natural-language description of the capability the agent needs"`
	TopK       int      `json:"top_k,omitempty" jsonschema:"maximum number of MCP servers to return (default 5)"`
	Categories []string `json:"categories,omitempty" jsonschema:"optional category filter; results match at least one of these"`
	Source     string   `json:"source,omitempty" jsonschema:"optional source filter, e.g. \"registry\""`
	MinScore   float64  `json:"min_score,omitempty" jsonschema:"optional confidence cutoff 0..1; results below it are dropped (default 0, no cutoff)"`
}

type SearchResult struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description"`
	Score       float32  `json:"score"`
	Confidence  float32  `json:"confidence"`
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

	hits, err := s.svc.Search(ctx, in.Query, topK, in.MinScore, store.Filter{
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
			Confidence:  h.Confidence,
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
	Found       bool       `json:"found"`
	MCP         *model.MCP `json:"mcp,omitempty"`
	Suggestions []string   `json:"suggestions,omitempty"`
}

// resolveMCP looks up an MCP by id, tolerating near-misses. On an exact hit it
// returns the record. On a miss it searches by the id and either auto-resolves a
// case-only difference (the same canonical id in different case) or returns nil
// plus up to a few suggested ids the caller most likely meant — so a slightly
// wrong id yields a usable hint instead of a bare "not found".
func (s *service) resolveMCP(ctx context.Context, id string) (*model.MCP, []string, error) {
	m, err := s.svc.Store.Get(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if m != nil {
		return m, nil, nil
	}
	hits, err := s.svc.Search(ctx, id, 5, 0, store.Filter{})
	if err != nil {
		return nil, nil, nil // best-effort: a search failure just means no hint
	}
	sugg := make([]string, 0, len(hits))
	for i := range hits {
		if strings.EqualFold(hits[i].MCP.ID, id) {
			mm := hits[i].MCP
			return &mm, nil, nil // same id, different case — resolve it
		}
		sugg = append(sugg, hits[i].MCP.ID)
	}
	return nil, sugg, nil
}

func (s *service) getMCPDetails(ctx context.Context, _ *mcp.CallToolRequest, in DetailsInput) (*mcp.CallToolResult, DetailsOutput, error) {
	if in.ID == "" {
		return nil, DetailsOutput{}, fmt.Errorf("id must not be empty")
	}
	m, sugg, err := s.resolveMCP(ctx, in.ID)
	if err != nil {
		return nil, DetailsOutput{}, err
	}
	if m == nil {
		return nil, DetailsOutput{Found: false, Suggestions: sugg}, nil
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

// ---- call_mcp (router/proxy) ----

type CallInput struct {
	MCPID     string         `json:"mcp_id" jsonschema:"id of the MCP server to call (as returned by search_mcp)"`
	Tool      string         `json:"tool" jsonschema:"name of the tool to invoke on that MCP server"`
	Arguments map[string]any `json:"arguments,omitempty" jsonschema:"arguments object passed to the tool"`
}

type CallOutput struct {
	Found       bool     `json:"found"`
	Content     string   `json:"content,omitempty"`
	Structured  any      `json:"structured,omitempty"`
	IsError     bool     `json:"is_error,omitempty"`
	Suggestions []string `json:"suggestions,omitempty"`
}

func (s *service) callMCP(ctx context.Context, _ *mcp.CallToolRequest, in CallInput) (*mcp.CallToolResult, CallOutput, error) {
	if in.MCPID == "" || in.Tool == "" {
		return nil, CallOutput{}, fmt.Errorf("mcp_id and tool are required")
	}
	m, sugg, err := s.resolveMCP(ctx, in.MCPID)
	if err != nil {
		return nil, CallOutput{}, err
	}
	if m == nil {
		// Never guess and forward to a different server; hand back suggestions.
		return nil, CallOutput{Found: false, Suggestions: sugg}, nil
	}
	res, err := s.proxy.Call(ctx, *m, in.Tool, in.Arguments)
	// Flywheel: every call is an implicit signal — the agent selected this MCP,
	// and it was either unreachable, served-but-errored (usually the caller's
	// args), or a clean success. Attribute them differently so the prior tracks
	// the server's serviceability, not the agent's mistakes. Record under the
	// canonical id (m.ID), which may differ in case from what the caller passed.
	if s.svc.Usage != nil {
		s.svc.Usage.Record(m.ID, callOutcome(res, err))
	}
	if err != nil {
		return nil, CallOutput{}, err
	}
	return nil, CallOutput{Found: true, Content: res.Content, Structured: res.Structured, IsError: res.IsError}, nil
}

// callOutcome classifies a forwarded call for the flywheel: a transport/connect
// failure is the server's fault (unreachable), a tool-level error is usually the
// caller's (partial credit), and otherwise it's a clean success.
func callOutcome(res proxy.Result, err error) usage.Outcome {
	switch {
	case err != nil:
		return usage.OutcomeUnreachable
	case res.IsError:
		return usage.OutcomeToolError
	default:
		return usage.OutcomeSuccess
	}
}
