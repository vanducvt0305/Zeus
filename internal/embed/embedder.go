// Package embed turns text into vectors. The indexer and the MCP server both
// depend on the Embedder interface so the concrete model can be swapped via
// configuration without touching either of them.
//
// IMPORTANT: the indexer and the server must use the SAME embedder, otherwise
// query vectors live in a different space than stored vectors and search
// results become meaningless.
package embed

import "context"

// Embedder converts text into fixed-size vectors.
type Embedder interface {
	// Embed returns one vector per input text, in the same order.
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	// Dim is the dimensionality of the produced vectors.
	Dim() int
	// Name is a human-readable identifier of the model, used in logs.
	Name() string
}
