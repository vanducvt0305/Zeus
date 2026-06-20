// Command server runs the MCP discovery server over stdio. An MCP-capable
// agent launches this binary and calls its tools (search_mcp, get_mcp_details,
// list_categories) to discover other MCP servers.
package main

import (
	"context"
	"io"
	"log"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/vanducvt0305/zeus/internal/config"
	"github.com/vanducvt0305/zeus/internal/server"
)

func main() {
	log.SetPrefix("zeus-mcp: ")
	log.SetFlags(0)

	cfg := config.Load()

	svc, err := cfg.NewSearchService()
	if err != nil {
		log.Fatalf("search service: %v", err)
	}
	if c, ok := svc.Store.(io.Closer); ok {
		defer c.Close()
	}

	log.Printf("starting MCP discovery server (embedder=%s, hybrid=%t, reranker=%s, collection=%s)",
		svc.Embedder.Name(), cfg.Hybrid, cfg.Reranker, cfg.QdrantCollection)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srv := server.New("zeus-mcp-discovery", "0.1.0", svc)
	if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil {
		log.Fatalf("server stopped: %v", err)
	}
}
