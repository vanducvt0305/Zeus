// Package config centralizes all environment-driven settings and builds the
// shared components (embedder, store). The indexer and the server both load
// config the same way, which guarantees they agree on the embedder and the
// Qdrant collection — a hard requirement for search to work.
package config

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/vanducvt0305/zeus/internal/embed"
	"github.com/vanducvt0305/zeus/internal/enrich"
	"github.com/vanducvt0305/zeus/internal/extract"
	"github.com/vanducvt0305/zeus/internal/llm"
	"github.com/vanducvt0305/zeus/internal/proxy"
	"github.com/vanducvt0305/zeus/internal/rerank"
	"github.com/vanducvt0305/zeus/internal/search"
	"github.com/vanducvt0305/zeus/internal/source"
	"github.com/vanducvt0305/zeus/internal/sparse"
	"github.com/vanducvt0305/zeus/internal/store"
	"github.com/vanducvt0305/zeus/internal/trust"
	"github.com/vanducvt0305/zeus/internal/usage"
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

	// IndexPrune deletes a (re)indexed MCP's existing points before upserting,
	// so removed tools/queries don't linger as orphans.
	IndexPrune bool
	// IndexConcurrency bounds the LLM/network-bound enrich and trust stages.
	IndexConcurrency int

	ExtractTools        bool
	ExtractConcurrency  int
	ExtractTimeout      int    // seconds, per connection attempt
	ExtractAuthToken    string // global bearer token for tools/list probing
	ExtractCredentials  string // path to a per-server credentials JSON file
	ExtractAllowPrivate bool   // allow probing private/loopback addresses (unsafe)

	// Enricher selection: "heuristic" (offline default), "llm", or "none".
	Enricher string

	// LLM settings, used when Enricher == "llm" or Reranker == "llm".
	LLMProvider string // "anthropic" or "openai"
	LLMBaseURL  string
	LLMAPIKey   string
	LLMModel    string

	// Hybrid enables sparse (keyword) retrieval fused with dense at query time.
	Hybrid bool

	// Sparse encoder: "tf" (default, stateless) or "bm25" (corpus-aware IDF +
	// length normalization; stronger on large/boilerplate-heavy catalogs).
	// Switching modes requires re-indexing, since the stored document vectors
	// carry the document-side weighting.
	Sparse string
	// SparseStatsPath is where BM25 corpus stats are persisted by the indexer and
	// loaded by the server. Indexer and server must point at the same file.
	SparseStatsPath string
	// BM25K1/BM25B are the BM25 term-frequency saturation and length-normalization
	// hyperparameters.
	BM25K1 float64
	BM25B  float64

	// Reranker selection: "lexical" (offline default), "llm", or "none".
	Reranker string
	// RerankPool is how many first-stage candidates to rerank before truncating
	// to the requested top-k.
	RerankPool int

	// SearchCacheTTL (seconds) and SearchCacheSize bound the query-result cache.
	// TTL or size <= 0 disables it. The TTL caps how stale a cached result can be
	// relative to fresh index/usage state.
	SearchCacheTTL  int
	SearchCacheSize int

	// Trust scorer selection (index-time): "heuristic" (default), "llm", "none".
	Trust string
	// TrustWeight (search-time) is how much the stored trust prior influences
	// final ranking, 0..1. 0 disables the blend.
	TrustWeight float64
	// CoverageWeight (search-time) is how much matching on several of an MCP's
	// representations (tools/synthetic queries) boosts it, 0..1. 0 disables it.
	CoverageWeight float64

	// Proxy: the server's call_mcp tool forwards calls to discovered MCPs.
	ProxyEnabled bool
	ProxyTimeout int // seconds, per connect+call

	// Server transport: "stdio" (default) or "http" (hosted gateway).
	Transport string
	HTTPAddr  string

	// Usage flywheel: record call_mcp outcomes and feed them into ranking.
	UsagePath   string  // JSON file to persist tallies; empty = in-memory only
	UsageWeight float64 // 0..1 ranking influence of the usage prior

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

		IndexPrune:          envBool("INDEX_PRUNE", true),
		IndexConcurrency:    envInt("INDEX_CONCURRENCY", 8),
		ExtractTools:        envBool("EXTRACT_TOOLS", false),
		ExtractConcurrency:  envInt("EXTRACT_CONCURRENCY", 8),
		ExtractTimeout:      envInt("EXTRACT_TIMEOUT", 20),
		ExtractAuthToken:    env("EXTRACT_AUTH_TOKEN", ""),
		ExtractCredentials:  env("EXTRACT_CREDENTIALS", ""),
		ExtractAllowPrivate: envBool("EXTRACT_ALLOW_PRIVATE", false),

		Enricher:    env("ENRICHER", "heuristic"),
		LLMProvider: env("LLM_PROVIDER", "anthropic"),
		LLMBaseURL:  env("LLM_BASE_URL", ""),
		LLMAPIKey:   env("LLM_API_KEY", ""),
		LLMModel:    env("LLM_MODEL", "claude-haiku-4-5"),

		Hybrid:          envBool("HYBRID", true),
		Sparse:          env("SPARSE", "tf"),
		SparseStatsPath: env("SPARSE_STATS_PATH", "sparse_stats.json"),
		BM25K1:          envFloat("BM25_K1", sparse.DefaultK1),
		BM25B:           envFloat("BM25_B", sparse.DefaultB),
		Reranker:        env("RERANKER", "lexical"),
		RerankPool:      envInt("RERANK_POOL", 30),
		SearchCacheTTL:  envInt("SEARCH_CACHE_TTL", 60),
		SearchCacheSize: envInt("SEARCH_CACHE_SIZE", 512),

		Trust:          env("TRUST", "heuristic"),
		TrustWeight:    envFloat("TRUST_WEIGHT", 0.15),
		CoverageWeight: envFloat("COVERAGE_WEIGHT", 0.05),

		ProxyEnabled: envBool("PROXY_ENABLED", true),
		ProxyTimeout: envInt("PROXY_TIMEOUT", 30),

		Transport: env("TRANSPORT", "stdio"),
		HTTPAddr:  env("HTTP_ADDR", ":8080"),

		UsagePath:   env("USAGE_PATH", ""),
		UsageWeight: envFloat("USAGE_WEIGHT", 0.10),

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

// NewSparseEncoder builds the query-side sparse (keyword) encoder used for
// hybrid search. For "bm25" it loads the corpus stats the indexer persisted so
// queries get matching IDF weights; if those stats are missing (e.g. the server
// runs without the indexer's stats file) it logs and falls back to stateless TF
// rather than silently mis-weighting queries against BM25 documents.
func (c Config) NewSparseEncoder() sparse.Encoder {
	if strings.ToLower(c.Sparse) != "bm25" {
		return sparse.Lexical{}
	}
	stats, err := sparse.LoadStats(c.SparseStatsPath)
	if err != nil {
		log.Printf("sparse: bm25 requested but stats %q unavailable (%v); falling back to tf", c.SparseStatsPath, err)
		return sparse.Lexical{}
	}
	return sparse.NewBM25(stats, c.BM25K1, c.BM25B)
}

// NewSparseFitter builds the index-time sparse fitter. For "bm25" it returns a
// builder that computes and persists corpus stats during indexing; otherwise it
// returns the stateless TF encoder (which fits to itself).
func (c Config) NewSparseFitter() sparse.Fitter {
	if strings.ToLower(c.Sparse) != "bm25" {
		return sparse.Lexical{}
	}
	return sparse.BM25Builder{K1: c.BM25K1, B: c.BM25B, Path: c.SparseStatsPath}
}

// NewTrustScorer builds the configured index-time trust scorer.
func (c Config) NewTrustScorer() (trust.Scorer, error) {
	switch c.Trust {
	case "", "heuristic":
		return trust.Heuristic{}, nil
	case "none", "noop":
		return trust.Noop{}, nil
	case "llm":
		client, err := c.newLLMClient()
		if err != nil {
			return nil, err
		}
		return trust.NewLLM(client), nil
	default:
		return nil, fmt.Errorf("unknown TRUST %q (want \"heuristic\", \"llm\" or \"none\")", c.Trust)
	}
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

// NewProxy builds the call-forwarding proxy, or nil if disabled. It reuses the
// extractor's credentials and SSRF policy for reaching target servers.
func (c Config) NewProxy() (*proxy.Proxy, error) {
	if !c.ProxyEnabled {
		return nil, nil
	}
	creds, err := extract.LoadCredentials(c.ExtractCredentials, c.ExtractAuthToken)
	if err != nil {
		return nil, err
	}
	return proxy.New(creds, c.ExtractAllowPrivate, time.Duration(c.ProxyTimeout)*time.Second), nil
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
	return extract.NewRemoteWithAuth(time.Duration(c.ExtractTimeout)*time.Second, creds, c.ExtractAllowPrivate), nil
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
	svc := &search.Service{
		Embedder:       emb,
		Sparse:         c.NewSparseEncoder(),
		Store:          st,
		Reranker:       rr,
		Hybrid:         c.Hybrid,
		Pool:           c.RerankPool,
		TrustWeight:    c.TrustWeight,
		UsageWeight:    c.UsageWeight,
		CoverageWeight: c.CoverageWeight,
	}
	svc.EnableCache(time.Duration(c.SearchCacheTTL)*time.Second, c.SearchCacheSize)
	return svc, nil
}

// NewUsageRecorder builds the flywheel recorder (in-memory, persisted to
// UsagePath when set).
func (c Config) NewUsageRecorder() *usage.Memory {
	return usage.NewMemory(c.UsagePath)
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

func envFloat(key string, def float64) float64 {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}
