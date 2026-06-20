package enrich

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/vanducvt0305/zeus/internal/llm"
	"github.com/vanducvt0305/zeus/internal/model"
)

// LLM is the high-quality enricher. It asks a language model to rewrite an MCP
// into a capability card, generating the task language and synthetic queries
// that the heuristic enricher cannot invent. On any failure it falls back to
// the heuristic enricher so the indexing pipeline never stalls on one record.
type LLM struct {
	client   llm.Client
	fallback Heuristic
}

// NewLLM builds an LLM enricher backed by the given client.
func NewLLM(client llm.Client) *LLM {
	return &LLM{client: client}
}

func (e *LLM) Name() string { return "llm(" + e.client.Name() + ")" }

const enrichSystemPrompt = `You normalize Model Context Protocol (MCP) server metadata into a "capability card" for a semantic search index.

Agents will search this index in TASK language ("I need to extract tables from a PDF"), while MCP descriptions are written in CAPABILITY/marketing language. Your job is to bridge that gap.

Return ONLY a JSON object, no prose, no markdown fences, with exactly these keys:
{
  "summary": string,            // one neutral paragraph: what this MCP does and when to use it
  "tasks": [string],            // concrete jobs-to-be-done in agent-intent language, e.g. "cancel a hotel booking"
  "example_queries": [string],  // natural-language queries an agent would type that THIS MCP should answer
  "synonyms": [string],         // alternative terms for its domain and capabilities
  "categories": [string]        // 1-4 broad domain categories, lowercase, e.g. "web-search", "database"
}

Rules:
- 5-10 example_queries, each phrased the way a real agent would ask, varying vocabulary.
- Be specific to the actual capabilities; do not invent features that are not implied by the input.
- If information is sparse, infer conservatively from the name and any tools.`

type llmCard struct {
	Summary        string   `json:"summary"`
	Tasks          []string `json:"tasks"`
	ExampleQueries []string `json:"example_queries"`
	Synonyms       []string `json:"synonyms"`
	Categories     []string `json:"categories"`
}

func (e *LLM) Enrich(ctx context.Context, m model.MCP) (model.MCP, error) {
	prompt, err := buildUserPrompt(m)
	if err != nil {
		return e.fallback.Enrich(ctx, m)
	}

	raw, err := e.client.Complete(ctx, enrichSystemPrompt, prompt)
	if err != nil {
		// Surface the error but keep a usable record via the heuristic path.
		enriched, _ := e.fallback.Enrich(ctx, m)
		return enriched, fmt.Errorf("llm enrich %q: %w", m.ID, err)
	}

	card, err := parseCard(raw)
	if err != nil {
		enriched, _ := e.fallback.Enrich(ctx, m)
		return enriched, fmt.Errorf("parsing card for %q: %w", m.ID, err)
	}

	m.Enrichment = model.Enrichment{
		Summary:        strings.TrimSpace(card.Summary),
		Tasks:          card.Tasks,
		ExampleQueries: card.ExampleQueries,
		Synonyms:       card.Synonyms,
		Categories:     card.Categories,
		Enricher:       e.Name(),
	}
	return m, nil
}

// promptInput is the compact view of an MCP we hand to the model.
type promptInput struct {
	Name        string   `json:"name"`
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description"`
	Categories  []string `json:"categories,omitempty"`
	Tools       []struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
	} `json:"tools,omitempty"`
}

func buildUserPrompt(m model.MCP) (string, error) {
	in := promptInput{
		Name:        m.Name,
		Title:       m.Title,
		Description: m.Description,
		Categories:  m.Categories,
	}
	for _, t := range m.Tools {
		in.Tools = append(in.Tools, struct {
			Name        string `json:"name"`
			Description string `json:"description,omitempty"`
		}{Name: t.Name, Description: t.Description})
	}
	b, err := json.Marshal(in)
	if err != nil {
		return "", err
	}
	return "Create the capability card for this MCP server:\n\n" + string(b), nil
}

// parseCard extracts the JSON object from a model response, tolerating markdown
// code fences and surrounding prose.
func parseCard(raw string) (llmCard, error) {
	var card llmCard
	s := stripFences(raw)
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start < 0 || end <= start {
		return card, fmt.Errorf("no JSON object found in response")
	}
	if err := json.Unmarshal([]byte(s[start:end+1]), &card); err != nil {
		return card, err
	}
	return card, nil
}

func stripFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	return s
}
