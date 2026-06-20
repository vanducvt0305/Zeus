package source

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vanducvt0305/zeus/internal/model"
)

// DefaultGitHubQueries find MCP server repositories by topic.
var DefaultGitHubQueries = []string{
	"topic:mcp-server",
	"topic:model-context-protocol",
	"topic:mcp",
}

// GitHub discovers MCP servers by searching GitHub repositories and, when a
// repository publishes a root server.json, parsing it for connection details.
// Otherwise it builds a record from the repository's metadata (description,
// topics, homepage).
//
// A token (GITHUB_TOKEN) is optional but strongly recommended: the search API
// allows only ~10 requests/min unauthenticated versus 30/min authenticated.
type GitHub struct {
	token   string
	queries []string
	client  *http.Client
}

// NewGitHub builds a GitHub crawler. Empty queries use DefaultGitHubQueries.
func NewGitHub(token string, queries []string) *GitHub {
	if len(queries) == 0 {
		queries = DefaultGitHubQueries
	}
	return &GitHub{
		token:   token,
		queries: queries,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

func (g *GitHub) Name() string { return "github" }

type ghRepo struct {
	FullName string `json:"full_name"`
	Name     string `json:"name"`
	Owner    struct {
		Login string `json:"login"`
	} `json:"owner"`
	Description   string   `json:"description"`
	HTMLURL       string   `json:"html_url"`
	Homepage      string   `json:"homepage"`
	Topics        []string `json:"topics"`
	PushedAt      string   `json:"pushed_at"`
	Stars         int      `json:"stargazers_count"`
	DefaultBranch string   `json:"default_branch"`
	Archived      bool     `json:"archived"`
}

type ghSearchResp struct {
	TotalCount int      `json:"total_count"`
	Items      []ghRepo `json:"items"`
}

// githubMapConcurrency bounds parallel server.json fetches during mapping.
const githubMapConcurrency = 10

func (g *GitHub) Fetch(ctx context.Context, limit int) ([]model.MCP, error) {
	// First collect the distinct repositories to consider (cheap, ordered),
	// then map them to records concurrently (each map may fetch server.json).
	seen := make(map[string]struct{})
	var repos []ghRepo
collect:
	for _, q := range g.queries {
		found, err := g.searchAll(ctx, q, limit, len(repos))
		if err != nil {
			// Best-effort: a rate-limited or failing query should not discard
			// what other queries already found.
			log.Printf("github: query %q stopped early: %v", q, err)
		}
		for _, r := range found {
			if r.Archived || r.FullName == "" {
				continue
			}
			if _, dup := seen[r.FullName]; dup {
				continue
			}
			seen[r.FullName] = struct{}{}
			repos = append(repos, r)
			if limit > 0 && len(repos) >= limit {
				break collect
			}
		}
	}

	out := make([]model.MCP, len(repos))
	sem := make(chan struct{}, githubMapConcurrency)
	var wg sync.WaitGroup
	for i, r := range repos {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, r ghRepo) {
			defer wg.Done()
			defer func() { <-sem }()
			out[i] = g.repoToMCP(ctx, r)
		}(i, r)
	}
	wg.Wait()
	return out, nil
}

// searchAll paginates the repository search for one query. have is how many
// records the caller already holds, so we stop once limit is reached overall.
func (g *GitHub) searchAll(ctx context.Context, query string, limit, have int) ([]ghRepo, error) {
	const perPage = 100
	var repos []ghRepo
	for page := 1; ; page++ {
		// GitHub search returns at most 1000 results per query.
		if page*perPage > 1000 {
			break
		}
		resp, err := g.searchPage(ctx, query, page, perPage)
		if err != nil {
			return repos, err
		}
		repos = append(repos, resp.Items...)
		if len(resp.Items) < perPage {
			break // last page
		}
		if limit > 0 && have+len(repos) >= limit {
			break
		}
	}
	return repos, nil
}

func (g *GitHub) searchPage(ctx context.Context, query string, page, perPage int) (ghSearchResp, error) {
	u, _ := url.Parse("https://api.github.com/search/repositories")
	qs := u.Query()
	qs.Set("q", query)
	qs.Set("per_page", strconv.Itoa(perPage))
	qs.Set("page", strconv.Itoa(page))
	qs.Set("sort", "stars")
	u.RawQuery = qs.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return ghSearchResp{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if g.token != "" {
		req.Header.Set("Authorization", "Bearer "+g.token)
	}

	resp, err := g.client.Do(req)
	if err != nil {
		return ghSearchResp{}, fmt.Errorf("github search: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != http.StatusOK {
		return ghSearchResp{}, fmt.Errorf("github search returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var parsed ghSearchResp
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ghSearchResp{}, fmt.Errorf("decoding github search: %w", err)
	}
	return parsed, nil
}

// repoToMCP builds a record from repository metadata, overlaying a root
// server.json when present (which adds transports/packages for extraction).
func (g *GitHub) repoToMCP(ctx context.Context, r ghRepo) model.MCP {
	var m model.MCP
	if sj, ok := g.fetchServerJSON(ctx, r); ok {
		m = sj.toMCP("github")
	}
	if m.Name == "" {
		m.Name = fmt.Sprintf("io.github.%s/%s", strings.ToLower(r.Owner.Login), strings.ToLower(r.Name))
	}
	if m.ID == "" {
		m.ID = m.Name
	}
	if m.Title == "" {
		m.Title = r.Name
	}
	if m.Description == "" {
		m.Description = r.Description
	}
	if m.Repository == "" {
		m.Repository = r.HTMLURL
	}
	if m.Homepage == "" {
		m.Homepage = r.Homepage
	}
	m.Source = "github"
	m.UpdatedAt = r.PushedAt
	if m.Popularity == 0 {
		m.Popularity = r.Stars
	}
	m.Categories = mergeCategories(m.Categories, filterTopics(r.Topics))
	return m
}

// fetchServerJSON attempts to read a root server.json from the repo's default
// branch over the raw CDN (not subject to the API rate limit).
func (g *GitHub) fetchServerJSON(ctx context.Context, r ghRepo) (serverJSON, bool) {
	branch := r.DefaultBranch
	if branch == "" {
		branch = "main"
	}
	rawURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/server.json", r.FullName, branch)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return serverJSON{}, false
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return serverJSON{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return serverJSON{}, false
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var sj serverJSON
	if err := json.Unmarshal(body, &sj); err != nil {
		return serverJSON{}, false
	}
	return sj, true
}

// genericTopics carry no discriminative signal as categories.
var genericTopics = map[string]struct{}{
	"mcp": {}, "mcp-server": {}, "mcp-servers": {}, "model-context-protocol": {},
	"modelcontextprotocol": {}, "ai": {}, "llm": {}, "claude": {}, "anthropic": {},
}

func filterTopics(topics []string) []string {
	var out []string
	for _, t := range topics {
		if _, generic := genericTopics[strings.ToLower(t)]; generic {
			continue
		}
		out = append(out, t)
	}
	return out
}

func mergeCategories(a, b []string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, c := range append(append([]string{}, a...), b...) {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	return out
}
