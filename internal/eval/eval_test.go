package eval

import (
	"context"
	"math"
	"testing"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

// rankFromTable returns a RankFunc that serves canned results per query.
func rankFromTable(table map[string][]string) RankFunc {
	return func(_ context.Context, query string, topK int) ([]string, error) {
		ids := table[query]
		if len(ids) > topK {
			ids = ids[:topK]
		}
		return ids, nil
	}
}

func TestMetricsPerfectAndMiss(t *testing.T) {
	items := []GoldenItem{
		{Query: "a", Expected: []string{"x"}}, // perfect: x at rank 1
		{Query: "b", Expected: []string{"y"}}, // y at rank 2
		{Query: "c", Expected: []string{"z"}}, // missing entirely
	}
	table := map[string][]string{
		"a": {"x", "p", "q"},
		"b": {"p", "y", "q"},
		"c": {"p", "q", "r"},
	}

	rep, err := Run(context.Background(), items, rankFromTable(table), 3)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Hit@1: only query "a" has the expected id at rank 1 → 1/3.
	if !approx(rep.HitAt1, 1.0/3.0) {
		t.Errorf("Hit@1 = %v, want 1/3", rep.HitAt1)
	}
	// Recall@3: a=1, b=1, c=0 → 2/3.
	if !approx(rep.RecallAtK, 2.0/3.0) {
		t.Errorf("Recall@3 = %v, want 2/3", rep.RecallAtK)
	}
	// MRR: a=1/1, b=1/2, c=0 → (1 + 0.5 + 0)/3.
	if !approx(rep.MRR, (1.0+0.5+0.0)/3.0) {
		t.Errorf("MRR = %v, want 0.5", rep.MRR)
	}
	// nDCG@3: a=1 (rank1), b=(1/log2(3))/1, c=0 → mean.
	wantNDCG := (1.0 + (1.0/math.Log2(3)) + 0.0) / 3.0
	if !approx(rep.NDCG, wantNDCG) {
		t.Errorf("nDCG@3 = %v, want %v", rep.NDCG, wantNDCG)
	}
}

func TestMetricsAllPerfect(t *testing.T) {
	items := []GoldenItem{
		{Query: "a", Expected: []string{"x"}},
		{Query: "b", Expected: []string{"y"}},
	}
	table := map[string][]string{"a": {"x"}, "b": {"y"}}
	rep, err := Run(context.Background(), items, rankFromTable(table), 5)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for name, got := range map[string]float64{
		"Hit@1": rep.HitAt1, "Recall": rep.RecallAtK, "MRR": rep.MRR, "nDCG": rep.NDCG,
	} {
		if !approx(got, 1.0) {
			t.Errorf("%s = %v, want 1.0", name, got)
		}
	}
}
