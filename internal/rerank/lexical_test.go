package rerank

import (
	"context"
	"testing"

	"github.com/vanducvt0305/zeus/internal/model"
	"github.com/vanducvt0305/zeus/internal/store"
)

func TestLexicalRerankPromotesBetterCoverage(t *testing.T) {
	// The relevant candidate is given the WORSE first-stage score and is placed
	// second, so only a working reranker can surface it.
	relevant := store.Hit{
		Score: 0.1,
		MCP: model.MCP{
			ID:          "gmail",
			Title:       "Gmail",
			Description: "Email automation.",
			Tools:       []model.Tool{{Name: "send_email", Description: "Compose and send an email message."}},
		},
	}
	distractor := store.Hit{
		Score: 0.9,
		MCP: model.MCP{
			ID:          "maps",
			Title:       "Google Maps",
			Description: "Routing and geocoding.",
			Tools:       []model.Tool{{Name: "directions"}},
		},
	}

	out, err := Lexical{}.Rerank(context.Background(), "send an email to a customer", []store.Hit{distractor, relevant})
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("want 2 hits, got %d", len(out))
	}
	if out[0].MCP.ID != "gmail" {
		t.Fatalf("reranker should promote 'gmail' to the top, got %q", out[0].MCP.ID)
	}
}

func TestLexicalRerankEmptyQueryNoPanic(t *testing.T) {
	hits := []store.Hit{{MCP: model.MCP{ID: "x"}}}
	out, err := Lexical{}.Rerank(context.Background(), "   ", hits)
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("want hits returned unchanged, got %d", len(out))
	}
}
