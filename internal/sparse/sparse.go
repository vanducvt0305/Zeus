// Package sparse produces sparse (keyword) vectors that complement the dense
// semantic embeddings. Dense vectors capture meaning but miss exact terms;
// sparse vectors capture exact tokens — tool names, technical keywords — that
// an agent often types verbatim. Fusing the two (see store hybrid search)
// recovers matches that either alone would lose.
//
// The encoder is stateless and deterministic: a term maps to a fixed hashed id
// with no corpus statistics. That means the indexer and the server always agree
// on the representation without sharing any state, at the cost of not using IDF
// (a natural future upgrade once corpus stats are persisted).
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

// Encoder turns text into a sparse vector.
type Encoder interface {
	Encode(text string) Vector
	Name() string
}

// Lexical is a stateless term-frequency encoder with sublinear TF weighting and
// L2 normalization. Stopwords are dropped to reduce noise.
type Lexical struct{}

func (Lexical) Name() string { return "lexical-tf" }

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
