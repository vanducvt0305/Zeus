package search

import (
	"testing"

	"github.com/vanducvt0305/zeus/internal/model"
	"github.com/vanducvt0305/zeus/internal/store"
)

func TestBlendTrustPromotesTrustedAmongClose(t *testing.T) {
	// Two near-adjacent results; the second is much more trustworthy. A
	// meaningful weight should promote it above the first.
	hits := []store.Hit{
		{MCP: model.MCP{ID: "low-trust", Trust: 0.1}},
		{MCP: model.MCP{ID: "high-trust", Trust: 0.95}},
	}
	s := &Service{TrustWeight: 0.5}
	out := s.finalize(hits)
	if out[0].MCP.ID != "high-trust" {
		t.Fatalf("expected high-trust promoted to top, got %q", out[0].MCP.ID)
	}
}

func TestBlendTrustDisabledKeepsOrder(t *testing.T) {
	hits := []store.Hit{
		{MCP: model.MCP{ID: "a", Trust: 0.1}},
		{MCP: model.MCP{ID: "b", Trust: 0.99}},
	}
	s := &Service{TrustWeight: 0} // disabled
	out := s.finalize(hits)
	if out[0].MCP.ID != "a" {
		t.Fatalf("with weight 0, order must be unchanged; got %q first", out[0].MCP.ID)
	}
}

func TestBlendTrustSmallWeightRespectsRelevance(t *testing.T) {
	// A small weight must not let a marginally-more-trusted item leapfrog a
	// clearly more relevant one several ranks above it.
	hits := []store.Hit{
		{MCP: model.MCP{ID: "top", Trust: 0.5}},
		{MCP: model.MCP{ID: "mid", Trust: 0.5}},
		{MCP: model.MCP{ID: "bottom", Trust: 0.8}},
	}
	s := &Service{TrustWeight: 0.15}
	out := s.finalize(hits)
	if out[0].MCP.ID != "top" {
		t.Fatalf("small weight should keep the most relevant on top, got %q", out[0].MCP.ID)
	}
}
