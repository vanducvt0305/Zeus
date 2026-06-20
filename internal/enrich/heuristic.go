package enrich

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/vanducvt0305/zeus/internal/model"
)

// Heuristic is an offline, dependency-free enricher. It derives tasks and
// example queries by humanizing tool names and extracting salient phrases from
// the description. It is far weaker than the LLM enricher — it cannot invent
// task language the metadata doesn't already contain — but it needs no API key
// and gives the multi-representation indexing something to work with out of the
// box. It is also the honest baseline to compare the LLM enricher against.
type Heuristic struct{}

func (Heuristic) Name() string { return "heuristic" }

func (h Heuristic) Enrich(_ context.Context, m model.MCP) (model.MCP, error) {
	e := model.Enrichment{Enricher: h.Name()}
	e.Summary = strings.TrimSpace(m.Description)

	taskSet := newOrderedSet()
	querySet := newOrderedSet()

	for _, t := range m.Tools {
		phrase := humanize(t.Name)
		if phrase == "" {
			continue
		}
		taskSet.add(phrase)
		querySet.add("how do I " + phrase)
		querySet.add("I need to " + phrase)
		if d := strings.TrimSpace(t.Description); d != "" {
			querySet.add(firstSentence(d))
		}
	}

	// Derive a couple of example queries from the server description itself, so
	// MCPs that ship no tool list still get query-shaped vectors.
	if s := firstSentence(m.Description); s != "" {
		querySet.add(s)
	}
	if title := strings.TrimSpace(m.Title); title != "" {
		querySet.add(title)
	}

	e.Tasks = taskSet.slice()
	e.ExampleQueries = querySet.slice()
	e.Synonyms = keywords(m.Title + " " + m.Description)

	m.Enrichment = e
	return m, nil
}

var (
	splitter = regexp.MustCompile(`[_\-./]+`)
	camel    = regexp.MustCompile(`([a-z0-9])([A-Z])`)
	nonWord  = regexp.MustCompile(`[^a-zA-Z0-9 ]+`)
)

// humanize turns a tool identifier like "search_products" or "getUserById"
// into a readable phrase like "search products" / "get user by id".
func humanize(name string) string {
	s := splitter.ReplaceAllString(name, " ")
	s = camel.ReplaceAllString(s, "$1 $2")
	s = nonWord.ReplaceAllString(s, " ")
	s = strings.ToLower(strings.Join(strings.Fields(s), " "))
	return s
}

func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, ".!?\n"); i > 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// stopwords are common terms that carry little discriminative signal.
var stopwords = map[string]struct{}{
	"the": {}, "and": {}, "for": {}, "with": {}, "that": {}, "this": {},
	"from": {}, "your": {}, "you": {}, "are": {}, "can": {}, "use": {},
	"using": {}, "via": {}, "into": {}, "over": {}, "all": {}, "any": {},
	"mcp": {}, "server": {}, "tool": {}, "tools": {}, "api": {}, "data": {},
}

// keywords returns up to a handful of salient lowercase terms from text.
func keywords(text string) []string {
	text = nonWord.ReplaceAllString(strings.ToLower(text), " ")
	seen := make(map[string]struct{})
	var out []string
	for _, w := range strings.Fields(text) {
		if len(w) < 4 {
			continue
		}
		if _, stop := stopwords[w]; stop {
			continue
		}
		if _, ok := seen[w]; ok {
			continue
		}
		seen[w] = struct{}{}
		out = append(out, w)
	}
	sort.Strings(out)
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

// orderedSet preserves insertion order while de-duplicating.
type orderedSet struct {
	seen map[string]struct{}
	vals []string
}

func newOrderedSet() *orderedSet { return &orderedSet{seen: map[string]struct{}{}} }

func (o *orderedSet) add(s string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return
	}
	if _, ok := o.seen[s]; ok {
		return
	}
	o.seen[s] = struct{}{}
	o.vals = append(o.vals, s)
}

func (o *orderedSet) slice() []string { return o.vals }
