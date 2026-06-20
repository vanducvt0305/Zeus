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
	"log"

	"github.com/vanducvt0305/zeus/internal/config"
	"github.com/vanducvt0305/zeus/internal/index"
	"github.com/vanducvt0305/zeus/internal/source"
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

	src := source.NewRegistry(cfg.RegistryURL)
	ix := index.New(src, emb, st)

	log.Printf("indexing (embedder=%s, dim=%d, collection=%s)", emb.Name(), emb.Dim(), cfg.QdrantCollection)
	n, err := ix.Run(context.Background(), *limit)
	if err != nil {
		log.Fatalf("indexing failed: %v", err)
	}
	log.Printf("done: indexed %d MCP servers", n)
}
