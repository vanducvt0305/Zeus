package sparse

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
)

// Default BM25 hyperparameters. k1 controls term-frequency saturation; b
// controls how strongly document length normalizes the score.
const (
	DefaultK1 = 1.2
	DefaultB  = 0.75
)

// Stats are the corpus statistics BM25 needs: the document count, the average
// document length (in non-stopword tokens), and each term's document frequency
// (how many documents contain it). They are computed once at index time and
// reused at query time so the indexer's document weights and the server's query
// weights describe the same corpus.
type Stats struct {
	N     int            `json:"n"`
	AvgDL float64        `json:"avgdl"`
	DF    map[uint32]int `json:"df"`
}

// ComputeStats derives BM25 corpus statistics from the document texts. Documents
// that are empty after tokenizing/stopword-removal are ignored (they carry no
// keyword signal and would only deflate the average length).
func ComputeStats(docs []string) *Stats {
	df := make(map[uint32]int)
	var totalLen, n int
	for _, d := range docs {
		counts, docLen := tokenizeCounts(d)
		if docLen == 0 {
			continue
		}
		n++
		totalLen += docLen
		for id := range counts {
			df[id]++
		}
	}
	avg := 0.0
	if n > 0 {
		avg = float64(totalLen) / float64(n)
	}
	return &Stats{N: n, AvgDL: avg, DF: df}
}

// Save writes the stats to path as JSON, creating/truncating the file.
func (s *Stats) Save(path string) error {
	raw, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("marshaling sparse stats: %w", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return fmt.Errorf("writing sparse stats %q: %w", path, err)
	}
	return nil
}

// LoadStats reads stats previously written by Save.
func LoadStats(path string) (*Stats, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Stats
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("decoding sparse stats %q: %w", path, err)
	}
	if s.DF == nil {
		s.DF = map[uint32]int{}
	}
	return &s, nil
}

// BM25Builder is a Fitter that produces a corpus-fitted BM25 encoder. Fit
// computes the corpus statistics, optionally persists them to Path (so the
// server can build a matching query-side encoder), and returns the encoder used
// to weight documents at index time.
type BM25Builder struct {
	K1   float64
	B    float64
	Path string // where to persist Stats; empty = don't persist
}

func (b BM25Builder) Name() string { return "bm25" }

func (b BM25Builder) Fit(docs []string) (Encoder, error) {
	stats := ComputeStats(docs)
	if b.Path != "" {
		if err := stats.Save(b.Path); err != nil {
			return nil, err
		}
	}
	return NewBM25(stats, b.K1, b.B), nil
}

// BM25 is a corpus-aware sparse encoder. The document side (EncodeDoc) stores
// length-saturated term frequencies; the query side (Encode) applies inverse
// document frequency. Qdrant scores sparse vectors by dot product, so the dot
// product of a query and a document vector equals the BM25 score:
//
//	sum_t  idf(t) * tf(t)*(k1+1) / (tf(t) + k1*(1 - b + b*|d|/avgdl))
//
// Unlike Lexical, BM25 vectors are deliberately not L2-normalized — the raw
// magnitudes are the score.
type BM25 struct {
	stats *Stats
	k1    float64
	b     float64
}

// NewBM25 builds an encoder from corpus stats, defaulting k1/b when unset.
func NewBM25(stats *Stats, k1, b float64) *BM25 {
	if k1 <= 0 {
		k1 = DefaultK1
	}
	if b < 0 || b > 1 {
		b = DefaultB
	}
	if stats == nil {
		stats = &Stats{DF: map[uint32]int{}}
	}
	if stats.DF == nil {
		stats.DF = map[uint32]int{}
	}
	return &BM25{stats: stats, k1: k1, b: b}
}

func (e *BM25) Name() string { return "bm25" }

// EncodeDoc weights a stored document: saturated, length-normalized TF, no IDF
// (the IDF is applied on the query side).
func (e *BM25) EncodeDoc(text string) Vector {
	counts, docLen := tokenizeCounts(text)
	if docLen == 0 {
		return Vector{}
	}
	avgdl := e.stats.AvgDL
	if avgdl <= 0 {
		avgdl = float64(docLen)
	}
	norm := e.k1 * (1 - e.b + e.b*float64(docLen)/avgdl)
	v := Vector{
		Indices: make([]uint32, 0, len(counts)),
		Values:  make([]float32, 0, len(counts)),
	}
	for id, tf := range counts {
		w := float64(tf) * (e.k1 + 1) / (float64(tf) + norm)
		v.Indices = append(v.Indices, id)
		v.Values = append(v.Values, float32(w))
	}
	return v
}

// Encode weights a query: each distinct term gets its IDF. Terms unseen in the
// corpus (df 0) are dropped — they can only match a stored document by hash
// collision, and including them would add noise. Query term frequency is taken
// as 1, the usual BM25 choice for short queries.
func (e *BM25) Encode(text string) Vector {
	counts, _ := tokenizeCounts(text)
	if len(counts) == 0 {
		return Vector{}
	}
	v := Vector{
		Indices: make([]uint32, 0, len(counts)),
		Values:  make([]float32, 0, len(counts)),
	}
	for id := range counts {
		df := e.stats.DF[id]
		if df == 0 {
			continue
		}
		v.Indices = append(v.Indices, id)
		v.Values = append(v.Values, float32(idf(e.stats.N, df)))
	}
	return v
}

// idf is the BM25 (Robertson-Spärck-Jones) inverse document frequency, with the
// +1 inside the log keeping it non-negative even for very common terms.
func idf(n, df int) float64 {
	return math.Log(1 + (float64(n)-float64(df)+0.5)/(float64(df)+0.5))
}

// tokenizeCounts returns the term-frequency map (by hashed term id) and the
// document length in non-stopword tokens. It shares Lexical's tokenizer and
// stopword list so document and query terms map to the same ids.
func tokenizeCounts(text string) (map[uint32]int, int) {
	counts := make(map[uint32]int)
	n := 0
	for _, tok := range tokenize(text) {
		if _, stop := stopwords[tok]; stop {
			continue
		}
		counts[termID(tok)]++
		n++
	}
	return counts, n
}
