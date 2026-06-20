// Package config centralizes all environment-driven settings and builds the
// shared components (embedder, store). The indexer and the server both load
// config the same way, which guarantees they agree on the embedder and the
// Qdrant collection — a hard requirement for search to work.
package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/vanducvt0305/zeus/internal/embed"
	"github.com/vanducvt0305/zeus/internal/store"
)

// Config holds every tunable value, sourced from environment variables.
type Config struct {
	QdrantHost       string
	QdrantPort       int
	QdrantAPIKey     string
	QdrantCollection string

	// Embedder selection: "hash" (offline default) or "openai" (any
	// OpenAI-compatible endpoint: OpenAI, Voyage, Ollama, TEI).
	Embedder    string
	EmbedBaseURL string
	EmbedAPIKey  string
	EmbedModel   string
	EmbedDim     int

	RegistryURL string
}

// Load reads configuration from the environment, applying defaults that make
// the project run end-to-end with zero external setup (hash embedder + local
// Qdrant).
func Load() Config {
	return Config{
		QdrantHost:       env("QDRANT_HOST", "localhost"),
		QdrantPort:       envInt("QDRANT_PORT", 6334),
		QdrantAPIKey:     env("QDRANT_API_KEY", ""),
		QdrantCollection: env("QDRANT_COLLECTION", "mcps"),

		Embedder:     env("EMBEDDER", "hash"),
		EmbedBaseURL: env("EMBED_BASE_URL", "http://localhost:11434/v1"),
		EmbedAPIKey:  env("EMBED_API_KEY", ""),
		EmbedModel:   env("EMBED_MODEL", "nomic-embed-text"),
		EmbedDim:     envInt("EMBED_DIM", 256),

		RegistryURL: env("REGISTRY_URL", ""),
	}
}

// Embedder builds the configured embedder.
func (c Config) NewEmbedder() (embed.Embedder, error) {
	switch c.Embedder {
	case "", "hash":
		return embed.NewHash(c.EmbedDim), nil
	case "openai":
		if c.EmbedModel == "" {
			return nil, fmt.Errorf("EMBED_MODEL is required when EMBEDDER=openai")
		}
		return embed.NewOpenAI(c.EmbedBaseURL, c.EmbedAPIKey, c.EmbedModel, c.EmbedDim), nil
	default:
		return nil, fmt.Errorf("unknown EMBEDDER %q (want \"hash\" or \"openai\")", c.Embedder)
	}
}

// NewStore builds the configured Qdrant store.
func (c Config) NewStore() (store.Store, error) {
	return store.NewQdrant(c.QdrantHost, c.QdrantPort, c.QdrantAPIKey, c.QdrantCollection)
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
