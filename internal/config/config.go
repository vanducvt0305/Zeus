// Package config centralizes all environment-driven settings and builds the
// shared components (embedder, store). The indexer and the server both load
// config the same way, which guarantees they agree on the embedder and the
// Qdrant collection — a hard requirement for search to work.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/vanducvt0305/zeus/internal/embed"
	"github.com/vanducvt0305/zeus/internal/enrich"
	"github.com/vanducvt0305/zeus/internal/extract"
	"github.com/vanducvt0305/zeus/internal/llm"
	"github.com/vanducvt0305/zeus/internal/rerank"
	"github.com/vanducvt0305/zeus/internal/search"
	"github.com/vanducvt0305/zeus/internal/source"
	"github.com/vanducvt0305/zeus/internal/sparse"
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
	Embedder     string
	EmbedBaseURL string
	EmbedAPIKey  string
	EmbedModel   string
	EmbedDim     int

	// ExtractTools, when true, connects to each server's remote endpoint and
	// calls tools/list to recover its real tools before enrichment/indexing.
	ExtractTools       bool
	ExtractConcurrency int
	ExtractTimeout     int    // seconds, per connection attempt
	ExtractAuthToken   string // global bearer token for tools/list probing
	ExtractCredentials string // path to a per-server credentials JSON file

	// Enricher selection: "heuristic" (offline default), "llm", or "none".
	Enricher string

	// LLM settings, used when Enricher == "llm" or Reranker == "llm".
	LLMProvider string // "anthropic" or "openai"
	LLMBaseURL  string
	LLMAPIKey   string
	LLMModel    string

	// Hybrid enables sparse (keyword) retrieval fused with dense at query time.
	Hybrid bool

	// Reranker selection: "lexical" (offline default), "llm", or "none".
	Reranker string
	// RerankPool is how many first-stage candidates to rerank before truncating
	// to the requested top-k.
	RerankPool int

	// Source selection for the indexer: "registry" (default), "github", "file".
	Source        string
	RegistryURL   string
	GitHubToken   string
	GitHubQueries []string
	SourceFile    string
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

		ExtractTools:       envBool("EXTRACT_TOOLS", false),
		ExtractConcurrency: envInt("EXTRACT_CONCURRENCY", 8),
		ExtractTimeout:     envInt("EXTRACT_TIMEOUT", 20),
		ExtractAuthToken:   env("EXTRACT_AUTH_TOKEN", ""),
		ExtractCredentials: env("EXTRACT_CREDENTIALS", ""),

		Enricher:    env("ENRICHER", "heuristic"),
		LLMProvider: env("LLM_PROVIDER", "anthropic"),
		LLMBaseURL:  env("LLM_BASE_URL", ""),
		LLMAPIKey:   env("LLM_API_KEY", ""),
		LLMModel:    env("LLM_MODEL", "claude-haiku-4-5"),

		Hybrid:     envBool("HYBRID", true),
		Reranker:   env("RERANKER", "lexical"),
		RerankPool: envInt("RERANK_POOL", 30),

		Source:        env("SOURCE", "registry"),
		RegistryURL:   env("REGISTRY_URL", ""),
		GitHubToken:   env("GITHUB_TOKEN", ""),
		GitHubQueries: splitList(env("GITHUB_QUERIES", "")),
		SourceFile:    env("SOURCE_FILE", ""),
	}
}

// NewSource builds the configured indexing source. SOURCE may be a single name
// or a comma-separated list (e.g. "registry,github"); a list yields a Multi
// source whose duplicates are merged by the indexer's resolve stage.
func (c Config) NewSource() (source.Source, error) {
	names := splitList(c.Source)
	if len(names) == 0 {
		names = []string{"registry"}
	}
	srcs := make([]source.Source, 0, len(names))
	for _, n := range names {
		s, err := c.newSingleSource(n)
		if err != nil {
			return nil, err
		}
		srcs = append(srcs, s)
	}
	if len(srcs) == 1 {
		return srcs[0], nil
	}
	return source.NewMulti(srcs...), nil
}

func (c Config) newSingleSource(name string) (source.Source, error) {
	switch name {
	case "", "registry":
		return source.NewRegistry(c.RegistryURL), nil
	case "github":
		return source.NewGitHub(c.GitHubToken, c.GitHubQueries), nil
	case "file":
		if c.SourceFile == "" {
			return nil, fmt.Errorf("SOURCE_FILE is required when SOURCE includes \"file\"")
		}
		return source.NewFile(c.SourceFile), nil
	default:
		return nil, fmt.Errorf("unknown source %q (want \"registry\", \"github\" or \"file\")", name)
	}
}

// NewSparseEncoder builds the sparse (keyword) encoder used for hybrid search.
func (c Config) NewSparseEncoder() sparse.Encoder {
	return sparse.Lexical{}
}

// NewReranker builds the configured reranker.
func (c Config) NewReranker() (rerank.Reranker, error) {
	switch c.Reranker {
	case "", "lexical":
		return rerank.Lexical{}, nil
	case "none", "noop":
		return rerank.Noop{}, nil
	case "llm":
		client, err := c.newLLMClient()
		if err != nil {
			return nil, err
		}
		return rerank.NewLLM(client), nil
	default:
		return nil, fmt.Errorf("unknown RERANKER %q (want \"lexical\", \"llm\" or \"none\")", c.Reranker)
	}
}

// NewExtractor builds the configured tool extractor, loading any auth
// credentials for reaching servers that gate tools/list behind auth.
func (c Config) NewExtractor() (extract.Extractor, error) {
	if !c.ExtractTools {
		return extract.Noop{}, nil
	}
	creds, err := extract.LoadCredentials(c.ExtractCredentials, c.ExtractAuthToken)
	if err != nil {
		return nil, err
	}
	return extract.NewRemoteWithAuth(time.Duration(c.ExtractTimeout)*time.Second, creds), nil
}

// NewEnricher builds the configured enricher.
func (c Config) NewEnricher() (enrich.Enricher, error) {
	switch c.Enricher {
	case "", "heuristic":
		return enrich.Heuristic{}, nil
	case "none", "noop":
		return enrich.Noop{}, nil
	case "llm":
		client, err := c.newLLMClient()
		if err != nil {
			return nil, err
		}
		return enrich.NewLLM(client), nil
	default:
		return nil, fmt.Errorf("unknown ENRICHER %q (want \"heuristic\", \"llm\" or \"none\")", c.Enricher)
	}
}

func (c Config) newLLMClient() (llm.Client, error) {
	switch c.LLMProvider {
	case "", "anthropic":
		if c.LLMAPIKey == "" {
			return nil, fmt.Errorf("LLM_API_KEY is required for the anthropic provider")
		}
		return llm.NewAnthropic(c.LLMBaseURL, c.LLMAPIKey, c.LLMModel, 1024), nil
	case "openai":
		return llm.NewOpenAI(c.LLMBaseURL, c.LLMAPIKey, c.LLMModel), nil
	default:
		return nil, fmt.Errorf("unknown LLM_PROVIDER %q (want \"anthropic\" or \"openai\")", c.LLMProvider)
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

// NewSearchService assembles the full query-time pipeline (embedder, sparse
// encoder, store, reranker) from configuration.
func (c Config) NewSearchService() (*search.Service, error) {
	emb, err := c.NewEmbedder()
	if err != nil {
		return nil, err
	}
	st, err := c.NewStore()
	if err != nil {
		return nil, err
	}
	rr, err := c.NewReranker()
	if err != nil {
		return nil, err
	}
	return &search.Service{
		Embedder: emb,
		Sparse:   c.NewSparseEncoder(),
		Store:    st,
		Reranker: rr,
		Hybrid:   c.Hybrid,
		Pool:     c.RerankPool,
	}, nil
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

// splitList parses a comma-separated env value into a trimmed, non-empty slice.
func splitList(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func envBool(key string, def bool) bool {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}
