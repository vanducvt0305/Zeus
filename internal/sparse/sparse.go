// Package sparse produces sparse (keyword) vectors that complement the dense
// semantic embeddings. Dense vectors capture meaning but miss exact terms;
// sparse vectors capture exact tokens — tool names, technical keywords — that
// an agent often types verbatim. Fusing the two (see store hybrid search)
// recovers matches that either alone would lose.
//
// Two encoders are provided. Lexical is stateless TF: a term maps to a fixed
// hashed id with no corpus statistics, so the indexer and server always agree
// without sharing state. BM25 is the stronger, corpus-aware option: it weights
// rare terms higher (IDF) and saturates repeated terms and long documents, at
// the cost of needing document-frequency statistics computed at index time and
// reused at query time (see Fitter / Stats). Both split the weighting into a
// document side (EncodeDoc, stored) and a query side (Encode); for Lexical the
// two are identical, for BM25 the IDF lives on the query side so the dot product
// Qdrant computes between query and document reconstructs the BM25 score.
package sparse

import (
	"hash/fnv"
	"math"
	"strings"
	"unicode"
)

// Vector is a sparse vector in coordinate form: parallel indices and values,
// with unique, ascending-free indices (Qdrant only requires uniqueness).
type Vector struct {
	Indices []uint32
	Values  []float32
}

// Empty reports whether the vector has no terms.
func (v Vector) Empty() bool { return len(v.Indices) == 0 }

// Encoder turns text into a sparse vector. EncodeDoc weights a stored document;
// Encode weights a query. They differ only for corpus-aware encoders (BM25),
// where the document side carries the length-saturated term frequencies and the
// query side carries the inverse document frequencies.
type Encoder interface {
	EncodeDoc(text string) Vector
	Encode(text string) Vector
	Name() string
}

// Fitter derives an Encoder from the document corpus. Stateless encoders ignore
// the corpus and return themselves; BM25 computes document frequencies and the
// average document length (and persists them so the query side can match).
type Fitter interface {
	Fit(docs []string) (Encoder, error)
	Name() string
}

// Lexical is a stateless term-frequency encoder with sublinear TF weighting and
// L2 normalization. Stopwords are dropped to reduce noise. Its document and
// query sides are identical, and Fit is a no-op (it needs no corpus statistics).
type Lexical struct{}

func (Lexical) Name() string { return "lexical-tf" }

// Fit returns the encoder unchanged: Lexical uses no corpus statistics.
func (l Lexical) Fit(_ []string) (Encoder, error) { return l, nil }

// EncodeDoc is identical to Encode for Lexical (no document/query asymmetry).
func (l Lexical) EncodeDoc(text string) Vector { return l.Encode(text) }

func (Lexical) Encode(text string) Vector {
	counts := make(map[uint32]float32)
	for _, tok := range tokenize(text) {
		if _, stop := stopwords[tok]; stop {
			continue
		}
		counts[termID(tok)]++
	}
	if len(counts) == 0 {
		return Vector{}
	}
	v := Vector{
		Indices: make([]uint32, 0, len(counts)),
		Values:  make([]float32, 0, len(counts)),
	}
	var norm float64
	for id, tf := range counts {
		// Sublinear TF: 1 + log(tf) dampens repeated terms.
		w := float32(1 + math.Log(float64(tf)))
		v.Indices = append(v.Indices, id)
		v.Values = append(v.Values, w)
		norm += float64(w) * float64(w)
	}
	if norm > 0 {
		n := float32(math.Sqrt(norm))
		for i := range v.Values {
			v.Values[i] /= n
		}
	}
	return v
}

// termID hashes a token into the uint32 id space. Collisions are possible but
// rare enough not to matter for ranking.
func termID(tok string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(tok))
	return h.Sum32()
}

func tokenize(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
}

// stopwords are common terms that add little discriminative signal to keyword
// matching.
var stopwords = map[string]struct{}{
	"the": {}, "and": {}, "for": {}, "with": {}, "that": {}, "this": {},
	"from": {}, "your": {}, "you": {}, "are": {}, "can": {}, "use": {},
	"using": {}, "via": {}, "into": {}, "over": {}, "all": {}, "any": {},
	"a": {}, "an": {}, "of": {}, "to": {}, "in": {}, "on": {}, "is": {},
	"it": {}, "my": {}, "me": {}, "do": {}, "i": {},
}
