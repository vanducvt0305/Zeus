// Package llm is a tiny abstraction over a chat/completions model, used by the
// enrichment stage to turn terse MCP metadata into capability cards. Keeping it
// behind an interface means the provider (Anthropic, or any OpenAI-compatible
// endpoint including a local Ollama) is a config switch.
package llm

import "context"

// Client produces a single text completion for a system+user prompt.
type Client interface {
	// Complete returns the model's text response. Implementations should ask
	// the model to return only what the caller requested (e.g. raw JSON).
	Complete(ctx context.Context, system, user string) (string, error)
	// Name identifies the model, for logging.
	Name() string
}
