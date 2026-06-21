// Command server runs the MCP discovery server. It speaks MCP over stdio
// (default, for local launch by a host) or over streamable HTTP (TRANSPORT=http,
// for hosting as a remote gateway). Its tools — search_mcp, get_mcp_details,
// list_categories, and call_mcp — let agents discover and invoke other MCPs.
package main

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

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

	prx, err := cfg.NewProxy()
	if err != nil {
		log.Fatalf("proxy: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	newServer := func() *mcp.Server { return server.New("zeus-mcp-discovery", "0.1.0", svc, prx) }

	log.Printf("starting MCP discovery server (transport=%s, embedder=%s, hybrid=%t, reranker=%s, call_mcp=%t, collection=%s)",
		cfg.Transport, svc.Embedder.Name(), cfg.Hybrid, cfg.Reranker, prx != nil, cfg.QdrantCollection)

	switch cfg.Transport {
	case "", "stdio":
		if err := newServer().Run(ctx, &mcp.StdioTransport{}); err != nil {
			log.Fatalf("server stopped: %v", err)
		}
	case "http":
		if err := runHTTP(ctx, cfg.HTTPAddr, newServer); err != nil {
			log.Fatalf("server stopped: %v", err)
		}
	default:
		log.Fatalf("unknown TRANSPORT %q (want \"stdio\" or \"http\")", cfg.Transport)
	}
}

// runHTTP serves the MCP server over streamable HTTP, plus a /healthz endpoint,
// with graceful shutdown.
func runHTTP(ctx context.Context, addr string, newServer func() *mcp.Server) error {
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return newServer() }, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/", handler)

	srv := &http.Server{Addr: addr, Handler: mux}

	errc := make(chan error, 1)
	go func() {
		log.Printf("listening on %s (MCP at /, health at /healthz)", addr)
		errc <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
