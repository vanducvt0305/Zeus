package sparse

import (
	"path/filepath"
	"testing"
)

// dot computes the dot product of a query and a document sparse vector, the same
// quantity Qdrant scores by — for BM25 it equals the BM25 score.
func dot(q, d Vector) float64 {
	idx := make(map[uint32]float32, len(d.Indices))
	for i, id := range d.Indices {
		idx[id] = d.Values[i]
	}
	var sum float64
	for i, id := range q.Indices {
		if dv, ok := idx[id]; ok {
			sum += float64(q.Values[i]) * float64(dv)
		}
	}
	return sum
}

func TestComputeStatsCountsDocFrequencies(t *testing.T) {
	docs := []string{
		"send email report",
		"read email report",
		"email report daily",
		"kubernetes cluster report",
	}
	st := ComputeStats(docs)
	if st.N != 4 {
		t.Fatalf("want N=4, got %d", st.N)
	}
	if df := st.DF[termID("report")]; df != 4 {
		t.Fatalf("report should appear in all 4 docs, df=%d", df)
	}
	if df := st.DF[termID("email")]; df != 3 {
		t.Fatalf("email df should be 3, got %d", df)
	}
	if df := st.DF[termID("kubernetes")]; df != 1 {
		t.Fatalf("kubernetes df should be 1, got %d", df)
	}
}

// TestBM25RareTermOutranksCommon is the whole point of IDF: a document matching
// a rare query term should outscore one matching a common query term, even when
// both match exactly one term of equal length.
func TestBM25RareTermOutranksCommon(t *testing.T) {
	docs := []string{
		"send email report", "read email report", "email report daily",
		"kubernetes cluster report",
	}
	enc := NewBM25(ComputeStats(docs), DefaultK1, DefaultB)

	query := enc.Encode("email kubernetes")
	common := enc.EncodeDoc("email report")    // matches the common term
	rare := enc.EncodeDoc("kubernetes report") // matches the rare term

	if dot(query, rare) <= dot(query, common) {
		t.Fatalf("rare-term match (%.3f) should outscore common-term match (%.3f)",
			dot(query, rare), dot(query, common))
	}
}

func TestBM25LengthNormalizationPenalizesLongDocs(t *testing.T) {
	// Same single occurrence of "alpha"; the longer document should weight it
	// lower because of BM25 length normalization.
	stats := &Stats{N: 100, AvgDL: 5, DF: map[uint32]int{termID("alpha"): 10}}
	enc := NewBM25(stats, DefaultK1, DefaultB)

	short := enc.EncodeDoc("alpha")
	long := enc.EncodeDoc("alpha beta gamma delta epsilon zeta eta theta")

	wShort := valueFor(short, termID("alpha"))
	wLong := valueFor(long, termID("alpha"))
	if !(wShort > wLong) {
		t.Fatalf("alpha should weigh more in the short doc (%.3f) than the long doc (%.3f)", wShort, wLong)
	}
}

func TestBM25QueryDropsUnseenTerms(t *testing.T) {
	enc := NewBM25(&Stats{N: 3, AvgDL: 3, DF: map[uint32]int{termID("email"): 2}}, DefaultK1, DefaultB)
	v := enc.Encode("email zqxjnonexistent")
	if len(v.Indices) != 1 {
		t.Fatalf("query should keep only the in-corpus term, got %d", len(v.Indices))
	}
	if v.Indices[0] != termID("email") {
		t.Fatalf("kept the wrong term id %d", v.Indices[0])
	}
}

func TestStatsSaveLoadRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.json")
	orig := ComputeStats([]string{"email report", "kubernetes cluster"})
	if err := orig.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := LoadStats(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.N != orig.N || len(got.DF) != len(orig.DF) {
		t.Fatalf("roundtrip mismatch: N %d/%d, DF %d/%d", got.N, orig.N, len(got.DF), len(orig.DF))
	}
	if got.DF[termID("email")] != 1 {
		t.Fatalf("DF for email lost in roundtrip: %d", got.DF[termID("email")])
	}
}

func TestBM25BuilderFitPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.json")
	b := BM25Builder{K1: DefaultK1, B: DefaultB, Path: path}
	enc, err := b.Fit([]string{"email report", "kubernetes cluster report"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := enc.(*BM25); !ok {
		t.Fatalf("Fit should return a *BM25, got %T", enc)
	}
	if _, err := LoadStats(path); err != nil {
		t.Fatalf("Fit should have persisted stats: %v", err)
	}
}

func valueFor(v Vector, id uint32) float64 {
	for i, got := range v.Indices {
		if got == id {
			return float64(v.Values[i])
		}
	}
	return 0
}
