package embed

import (
	"context"
	"hash/fnv"
	"math"
	"strings"
	"unicode"
)

// HashEmbedder is a dependency-free, offline embedder. It maps text into a
// hashed bag-of-words vector (the "hashing trick") and L2-normalizes the
// result. It needs no network, no API key, and no model download, which makes
// it the default for a zero-setup quick start and for tests.
//
// It is NOT semantically strong: it captures lexical overlap, not meaning.
// Swap in OpenAIEmbedder (pointed at Ollama, a local TEI server, OpenAI, or
// Voyage) for real semantic quality.
type HashEmbedder struct {
	dim int
}

// NewHash returns a HashEmbedder producing vectors of the given dimension.
func NewHash(dim int) *HashEmbedder {
	if dim <= 0 {
		dim = 256
	}
	return &HashEmbedder{dim: dim}
}

func (e *HashEmbedder) Dim() int     { return e.dim }
func (e *HashEmbedder) Name() string { return "hash(local)" }

func (e *HashEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = e.embedOne(t)
	}
	return out, nil
}

func (e *HashEmbedder) embedOne(text string) []float32 {
	vec := make([]float32, e.dim)
	for _, tok := range tokenize(text) {
		h := fnv.New32a()
		_, _ = h.Write([]byte(tok))
		sum := h.Sum32()
		idx := int(sum % uint32(e.dim))
		// Use the top bit to pick a sign, reducing collisions between
		// unrelated tokens that land in the same bucket.
		if sum&0x80000000 != 0 {
			vec[idx] -= 1
		} else {
			vec[idx] += 1
		}
	}
	normalize(vec)
	return vec
}

func tokenize(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
}

func normalize(vec []float32) {
	var sum float64
	for _, v := range vec {
		sum += float64(v) * float64(v)
	}
	if sum == 0 {
		return
	}
	norm := float32(math.Sqrt(sum))
	for i := range vec {
		vec[i] /= norm
	}
}
