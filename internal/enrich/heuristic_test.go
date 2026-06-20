package enrich

import (
	"context"
	"strings"
	"testing"

	"github.com/vanducvt0305/zeus/internal/model"
)

func TestHumanize(t *testing.T) {
	cases := map[string]string{
		"search_products": "search products",
		"getUserById":     "get user by id",
		"list-channels":   "list channels",
		"run_query":       "run query",
	}
	for in, want := range cases {
		if got := humanize(in); got != want {
			t.Errorf("humanize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHeuristicProducesQueryShapedText(t *testing.T) {
	m := model.MCP{
		Name:        "gmail-email",
		Title:       "Gmail",
		Description: "Email automation for Gmail mailboxes.",
		Tools: []model.Tool{
			{Name: "send_email", Description: "Compose and send an email message."},
		},
	}
	out, err := Heuristic{}.Enrich(context.Background(), m)
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	e := out.Enrichment
	if e.Enricher != "heuristic" {
		t.Errorf("Enricher = %q", e.Enricher)
	}
	if len(e.Tasks) == 0 || e.Tasks[0] != "send email" {
		t.Errorf("Tasks = %v, want first 'send email'", e.Tasks)
	}
	// Example queries should include an agent-phrased form of the tool.
	joined := strings.Join(e.ExampleQueries, " | ")
	if !strings.Contains(joined, "send email") {
		t.Errorf("ExampleQueries %v missing a 'send email' phrasing", e.ExampleQueries)
	}
}
