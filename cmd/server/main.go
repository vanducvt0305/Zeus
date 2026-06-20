// Command server runs the MCP discovery server over stdio. An MCP-capable
// agent launches this binary and calls its tools (search_mcp, get_mcp_details,
// list_categories) to discover other MCP servers.
package main

import (
	"context"
	"log"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/vanducvt0305/zeus/internal/config"
	"github.com/vanducvt0305/zeus/internal/server"
)

func main() {
	log.SetPrefix("zeus-mcp: ")
	log.SetFlags(0)

	cfg := config.Load()

	emb, err := cfg.NewEmbedder()
	if err != nil {
		log.Fatalf("embedder: %v", err)
	}
	st, err := cfg.NewStore()
	if err != nil {
		log.Fatalf("store: %v", err)
	}

	log.Printf("starting MCP discovery server (embedder=%s, collection=%s)", emb.Name(), cfg.QdrantCollection)

	srv := server.New("zeus-mcp-discovery", "0.1.0", emb, st)
	if err := srv.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("server stopped: %v", err)
	}
}
