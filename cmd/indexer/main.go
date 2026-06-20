// Command indexer crawls a source of MCP servers, embeds them, and writes them
// to the vector store. Run it to populate or refresh the index that the
// discovery server searches over.
//
// Usage:
//
//	indexer [-limit N]
//
// Configuration comes from the environment (see internal/config). It must use
// the SAME embedder settings as the server.
package main

import (
	"context"
	"flag"
	"io"
	"log"
	"os/signal"
	"syscall"

	"github.com/vanducvt0305/zeus/internal/config"
	"github.com/vanducvt0305/zeus/internal/index"
)

func main() {
	log.SetPrefix("zeus-indexer: ")
	log.SetFlags(0)

	limit := flag.Int("limit", 0, "maximum number of MCP servers to index (0 = all)")
	flag.Parse()

	cfg := config.Load()

	emb, err := cfg.NewEmbedder()
	if err != nil {
		log.Fatalf("embedder: %v", err)
	}
	st, err := cfg.NewStore()
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	if c, ok := st.(io.Closer); ok {
		defer c.Close()
	}

	enr, err := cfg.NewEnricher()
	if err != nil {
		log.Fatalf("enricher: %v", err)
	}

	src, err := cfg.NewSource()
	if err != nil {
		log.Fatalf("source: %v", err)
	}
	ext, err := cfg.NewExtractor()
	if err != nil {
		log.Fatalf("extractor: %v", err)
	}
	tr, err := cfg.NewTrustScorer()
	if err != nil {
		log.Fatalf("trust scorer: %v", err)
	}
	ix := index.New(src, ext, enr, emb, cfg.NewSparseEncoder(), st)
	ix.ExtractConcurrency = cfg.ExtractConcurrency
	ix.Concurrency = cfg.IndexConcurrency
	ix.Prune = cfg.IndexPrune
	ix.Trust = tr

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Printf("indexing (source=%s, extract=%t, enricher=%s, trust=%s, embedder=%s, dim=%d, collection=%s)", src.Name(), cfg.ExtractTools, enr.Name(), tr.Name(), emb.Name(), emb.Dim(), cfg.QdrantCollection)
	n, err := ix.Run(ctx, *limit)
	if err != nil {
		log.Fatalf("indexing failed: %v", err)
	}
	log.Printf("done: indexed %d MCP servers", n)
}
