package rerank

import (
	"context"
	"sort"
	"strings"
	"unicode"

	"github.com/vanducvt0305/zeus/internal/model"
	"github.com/vanducvt0305/zeus/internal/store"
)

// Lexical is an offline cross-encoder-style reranker. For each candidate it
// builds the full capability text (summary, tasks, tools, example queries,
// synonyms) and scores how completely the query's terms are covered by it.
// Unlike the vector retrieval, it reads every field of the candidate jointly
// with the query, so it can repair ordering mistakes. It needs no model and no
// network; the LLM reranker is the higher-quality drop-in.
type Lexical struct{}

func (Lexical) Name() string { return "lexical" }

func (Lexical) Rerank(_ context.Context, query string, hits []store.Hit) ([]store.Hit, error) {
	qTerms := termSet(query)
	if len(qTerms) == 0 {
		return hits, nil
	}

	type scored struct {
		hit   store.Hit
		score float64
		orig  float32
	}
	ranked := make([]scored, len(hits))
	for i, h := range hits {
		cand := termSet(candidateText(h.MCP))
		matched := 0
		for t := range qTerms {
			if _, ok := cand[t]; ok {
				matched++
			}
		}
		ranked[i] = scored{
			hit:   h,
			score: float64(matched) / float64(len(qTerms)), // query-term coverage
			orig:  h.Score,
		}
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		// Break ties by the first-stage retrieval score.
		return ranked[i].orig > ranked[j].orig
	})

	out := make([]store.Hit, len(ranked))
	for i, r := range ranked {
		// Carry the reranker's own relevance (query-term coverage, 0..1) on the
		// hit so the downstream blend can weigh by how strong each match is,
		// instead of treating every rank step as equal. The first-stage retrieval
		// score is not comparable across the dense/sparse fusion, so coverage is
		// the better post-rerank relevance signal.
		r.hit.Score = float32(r.score)
		out[i] = r.hit
	}
	return out, nil
}

// candidateText assembles everything known about an MCP into one string for
// joint scoring against the query.
func candidateText(m model.MCP) string {
	var b strings.Builder
	w := func(s string) {
		if s = strings.TrimSpace(s); s != "" {
			b.WriteString(s)
			b.WriteByte('\n')
		}
	}
	w(m.Title)
	w(m.Name)
	w(m.Description)
	w(m.Enrichment.Summary)
	w(strings.Join(m.Enrichment.Tasks, "\n"))
	w(strings.Join(m.Enrichment.ExampleQueries, "\n"))
	w(strings.Join(m.Enrichment.Synonyms, " "))
	w(strings.Join(m.AllCategories(), " "))
	for _, t := range m.Tools {
		w(t.Name)
		w(t.Description)
	}
	return b.String()
}

func termSet(text string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, tok := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}) {
		if _, stop := stopwords[tok]; stop {
			continue
		}
		out[tok] = struct{}{}
	}
	return out
}

var stopwords = map[string]struct{}{
	"the": {}, "and": {}, "for": {}, "with": {}, "that": {}, "this": {},
	"from": {}, "your": {}, "you": {}, "are": {}, "can": {}, "use": {},
	"using": {}, "via": {}, "into": {}, "over": {}, "all": {}, "any": {},
	"a": {}, "an": {}, "of": {}, "to": {}, "in": {}, "on": {}, "is": {},
	"it": {}, "my": {}, "me": {}, "do": {}, "i": {}, "want": {}, "need": {},
}
