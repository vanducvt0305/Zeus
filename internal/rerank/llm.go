package rerank

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/vanducvt0305/zeus/internal/llm"
	"github.com/vanducvt0305/zeus/internal/store"
)

// LLM reranks by asking a language model to judge the shortlist as a whole —
// the highest-quality reranker here, since the model reads the query and every
// candidate jointly. On any failure it falls back to the lexical reranker so a
// flaky model never degrades results below the offline baseline.
type LLM struct {
	client   llm.Client
	fallback Lexical
}

// NewLLM builds an LLM reranker backed by the given client.
func NewLLM(client llm.Client) *LLM { return &LLM{client: client} }

func (e *LLM) Name() string { return "llm(" + e.client.Name() + ")" }

const rerankSystemPrompt = `You rank candidate MCP (Model Context Protocol) servers by how well they can accomplish a user's task.

You are given the task and a numbered list of candidates. Return ONLY a JSON array of candidate numbers, ordered from most to least relevant, including every number exactly once. No prose, no markdown. Example: [3,1,2,4]`

func (e *LLM) Rerank(ctx context.Context, query string, hits []store.Hit) ([]store.Hit, error) {
	if len(hits) <= 1 {
		return hits, nil
	}

	prompt := buildRerankPrompt(query, hits)
	raw, err := e.client.Complete(ctx, rerankSystemPrompt, prompt)
	if err != nil {
		out, _ := e.fallback.Rerank(ctx, query, hits)
		return out, fmt.Errorf("llm rerank: %w", err)
	}

	order, err := parseOrder(raw, len(hits))
	if err != nil {
		out, _ := e.fallback.Rerank(ctx, query, hits)
		return out, fmt.Errorf("parsing rerank order: %w", err)
	}

	out := make([]store.Hit, 0, len(hits))
	used := make([]bool, len(hits))
	for _, idx := range order {
		out = append(out, hits[idx])
		used[idx] = true
	}
	// Append any candidate the model omitted, preserving input order.
	for i, h := range hits {
		if !used[i] {
			out = append(out, h)
		}
	}
	// The model returns only an order, not scores; attach a smooth, monotonic
	// relevance by position so the downstream blend follows the LLM's ranking
	// (and the first-stage scores, which are not comparable here, don't fight it).
	n := float32(len(out))
	for i := range out {
		out[i].Score = (n - float32(i)) / n
	}
	return out, nil
}

func buildRerankPrompt(query string, hits []store.Hit) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Task: %s\n\nCandidates:\n", query)
	for i, h := range hits {
		desc := h.MCP.Enrichment.Summary
		if strings.TrimSpace(desc) == "" {
			desc = h.MCP.Description
		}
		fmt.Fprintf(&b, "%d. %s — %s\n", i+1, h.MCP.DisplayName(), strings.TrimSpace(desc))
	}
	return b.String()
}

// parseOrder reads a JSON array of 1-based candidate numbers and converts it to
// validated 0-based indices.
func parseOrder(raw string, n int) ([]int, error) {
	s := raw
	start := strings.IndexByte(s, '[')
	end := strings.LastIndexByte(s, ']')
	if start < 0 || end <= start {
		return nil, fmt.Errorf("no JSON array found")
	}
	var nums []int
	if err := json.Unmarshal([]byte(s[start:end+1]), &nums); err != nil {
		return nil, err
	}
	out := make([]int, 0, len(nums))
	seen := make(map[int]bool)
	for _, num := range nums {
		idx := num - 1
		if idx < 0 || idx >= n || seen[idx] {
			continue
		}
		seen[idx] = true
		out = append(out, idx)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no valid indices in response")
	}
	return out, nil
}
