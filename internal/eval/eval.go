// Package eval is the measurement discipline for search quality. It runs a
// golden set of (query → expected MCP) pairs through a ranking function and
// reports standard information-retrieval metrics, so every change to indexing,
// enrichment, or retrieval can be judged by numbers instead of vibes.
package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
)

// GoldenItem is one labeled query: the IDs of the MCPs that are correct
// answers. The first ID is treated as the primary expected result.
type GoldenItem struct {
	Query    string   `json:"query"`
	Expected []string `json:"expected"`
}

// LoadGolden reads a golden set from a JSON file.
func LoadGolden(path string) ([]GoldenItem, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading golden set: %w", err)
	}
	var items []GoldenItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("decoding golden set: %w", err)
	}
	return items, nil
}

// RankFunc returns the ranked MCP IDs for a query, best first.
type RankFunc func(ctx context.Context, query string, topK int) ([]string, error)

// QueryResult holds the per-query outcome, useful for inspecting failures.
type QueryResult struct {
	Query     string
	Expected  []string
	Returned  []string
	Rank      int // 1-based rank of the first relevant result, 0 if none in top-K
	HitAtK    bool
}

// Report is the aggregate scorecard across the whole golden set.
type Report struct {
	K         int
	N         int
	RecallAtK float64
	MRR       float64
	NDCG      float64
	HitAt1    float64
	PerQuery  []QueryResult
}

// Run executes every golden query through rank and aggregates the metrics.
func Run(ctx context.Context, items []GoldenItem, rank RankFunc, k int) (Report, error) {
	if k <= 0 {
		k = 5
	}
	rep := Report{K: k, N: len(items)}
	if len(items) == 0 {
		return rep, nil
	}

	var sumRecall, sumMRR, sumNDCG, sumHit1 float64
	for _, it := range items {
		ids, err := rank(ctx, it.Query, k)
		if err != nil {
			return rep, fmt.Errorf("ranking %q: %w", it.Query, err)
		}
		exp := toSet(it.Expected)
		qr := QueryResult{Query: it.Query, Expected: it.Expected, Returned: ids}

		// First relevant rank (for MRR) and hit@k.
		for i, id := range ids {
			if _, ok := exp[id]; ok {
				qr.Rank = i + 1
				qr.HitAtK = true
				break
			}
		}

		sumRecall += recallAtK(ids, exp)
		if qr.Rank > 0 {
			sumMRR += 1.0 / float64(qr.Rank)
		}
		sumNDCG += ndcgAtK(ids, exp, k)
		if len(ids) > 0 {
			if _, ok := exp[ids[0]]; ok {
				sumHit1++
			}
		}
		rep.PerQuery = append(rep.PerQuery, qr)
	}

	n := float64(len(items))
	rep.RecallAtK = sumRecall / n
	rep.MRR = sumMRR / n
	rep.NDCG = sumNDCG / n
	rep.HitAt1 = sumHit1 / n
	return rep, nil
}

func recallAtK(ids []string, expected map[string]struct{}) float64 {
	if len(expected) == 0 {
		return 0
	}
	found := 0
	for _, id := range ids {
		if _, ok := expected[id]; ok {
			found++
		}
	}
	return float64(found) / float64(len(expected))
}

// ndcgAtK computes nDCG with binary relevance (1 if returned id is expected).
func ndcgAtK(ids []string, expected map[string]struct{}, k int) float64 {
	var dcg float64
	for i, id := range ids {
		if i >= k {
			break
		}
		if _, ok := expected[id]; ok {
			dcg += 1.0 / math.Log2(float64(i)+2.0)
		}
	}
	// Ideal DCG: all relevant items ranked first.
	ideal := len(expected)
	if ideal > k {
		ideal = k
	}
	var idcg float64
	for i := 0; i < ideal; i++ {
		idcg += 1.0 / math.Log2(float64(i)+2.0)
	}
	if idcg == 0 {
		return 0
	}
	return dcg / idcg
}

func toSet(ss []string) map[string]struct{} {
	m := make(map[string]struct{}, len(ss))
	for _, s := range ss {
		m[s] = struct{}{}
	}
	return m
}

// String renders the report as a compact, human-readable scorecard.
func (r Report) String() string {
	return fmt.Sprintf(
		"queries=%d  k=%d\n  Hit@1     %.3f\n  Recall@%d  %.3f\n  MRR       %.3f\n  nDCG@%d    %.3f",
		r.N, r.K, r.HitAt1, r.K, r.RecallAtK, r.MRR, r.K, r.NDCG,
	)
}
