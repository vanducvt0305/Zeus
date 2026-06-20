package trust

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/vanducvt0305/zeus/internal/llm"
	"github.com/vanducvt0305/zeus/internal/model"
)

// LLM augments the deterministic signals with a model's judgment of a server's
// quality and legitimacy. The final trust is the average of the deterministic
// score and the LLM's quality rating, so neither dominates. On any failure it
// falls back to the deterministic score.
type LLM struct {
	client llm.Client
}

// NewLLM builds an LLM trust scorer.
func NewLLM(client llm.Client) *LLM { return &LLM{client: client} }

func (e *LLM) Name() string { return "llm(" + e.client.Name() + ")" }

const trustSystemPrompt = `You assess the quality and trustworthiness of a Model Context Protocol (MCP) server for a discovery index, so that better servers rank above low-quality or suspicious ones.

Judge only from the provided metadata. Return ONLY a JSON object, no prose, no markdown:
{
  "quality": number,        // 0.0-1.0: clarity, specificity, and apparent usefulness/legitimacy
  "risk_flags": [string],   // e.g. "vague-description", "possible-spam", "no-clear-capability"; empty if none
  "rationale": string       // one short sentence
}

Be conservative: vague, empty, or spammy descriptions score low; clear, specific, capable servers score high.`

type trustCard struct {
	Quality   float64  `json:"quality"`
	RiskFlags []string `json:"risk_flags"`
	Rationale string   `json:"rationale"`
}

func (e *LLM) Score(ctx context.Context, m model.MCP) (model.MCP, error) {
	det := deterministic(m)

	prompt, err := buildPrompt(m)
	if err != nil {
		m.Trust = det
		return m, err
	}
	raw, err := e.client.Complete(ctx, trustSystemPrompt, prompt)
	if err != nil {
		m.Trust = det
		return m, fmt.Errorf("llm trust %q: %w", m.ID, err)
	}
	card, err := parseCard(raw)
	if err != nil {
		m.Trust = det
		return m, fmt.Errorf("parsing trust card for %q: %w", m.ID, err)
	}

	m.Trust = clamp01(0.5*det + 0.5*clamp01(card.Quality))
	m.TrustRationale = strings.TrimSpace(card.Rationale)
	m.TrustFlags = card.RiskFlags
	return m, nil
}

func buildPrompt(m model.MCP) (string, error) {
	view := struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Summary     string   `json:"summary,omitempty"`
		Tools       []string `json:"tools,omitempty"`
		Categories  []string `json:"categories,omitempty"`
		Popularity  int      `json:"popularity,omitempty"`
	}{
		Name:        m.Name,
		Description: m.Description,
		Summary:     m.Enrichment.Summary,
		Categories:  m.AllCategories(),
		Popularity:  m.Popularity,
	}
	for _, t := range m.Tools {
		view.Tools = append(view.Tools, t.Name)
	}
	b, err := json.Marshal(view)
	if err != nil {
		return "", err
	}
	return "Assess this MCP server:\n\n" + string(b), nil
}

func parseCard(raw string) (trustCard, error) {
	var card trustCard
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "```") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start < 0 || end <= start {
		return card, fmt.Errorf("no JSON object found")
	}
	if err := json.Unmarshal([]byte(s[start:end+1]), &card); err != nil {
		return card, err
	}
	return card, nil
}
