// Command eval measures search quality. It runs a golden set of (query →
// expected MCP) pairs against the index and prints Hit@1, Recall@k, MRR, and
// nDCG@k. Use it to compare configurations — most importantly, to quantify how
// much enrichment improves results:
//
//	# baseline: no enrichment
//	ENRICHER=none      QDRANT_COLLECTION=eval_none      eval -index
//	# offline enrichment
//	ENRICHER=heuristic QDRANT_COLLECTION=eval_heuristic eval -index
//	# LLM enrichment (needs LLM_API_KEY)
//	ENRICHER=llm       QDRANT_COLLECTION=eval_llm       eval -index
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"sort"

	"github.com/vanducvt0305/zeus/internal/config"
	"github.com/vanducvt0305/zeus/internal/eval"
	"github.com/vanducvt0305/zeus/internal/index"
	"github.com/vanducvt0305/zeus/internal/source"
	"github.com/vanducvt0305/zeus/internal/store"
)

func main() {
	log.SetPrefix("zeus-eval: ")
	log.SetFlags(0)

	fixtures := flag.String("fixtures", "eval/fixtures/mcps.json", "path to the fixture MCP catalog")
	golden := flag.String("golden", "eval/golden.json", "path to the golden query set")
	k := flag.Int("k", 5, "cutoff k for Recall@k / nDCG@k")
	doIndex := flag.Bool("index", false, "re-index the fixtures before evaluating")
	showFails := flag.Bool("fails", false, "print queries with no relevant result in the top-k")
	flag.Parse()

	ctx := context.Background()
	cfg := config.Load()

	emb, err := cfg.NewEmbedder()
	if err != nil {
		log.Fatalf("embedder: %v", err)
	}
	st, err := cfg.NewStore()
	if err != nil {
		log.Fatalf("store: %v", err)
	}

	if *doIndex {
		enr, err := cfg.NewEnricher()
		if err != nil {
			log.Fatalf("enricher: %v", err)
		}
		ix := index.New(source.NewFile(*fixtures), enr, emb, st)
		log.Printf("indexing fixtures (enricher=%s, embedder=%s, collection=%s)...", enr.Name(), emb.Name(), cfg.QdrantCollection)
		if _, err := ix.Run(ctx, 0); err != nil {
			log.Fatalf("indexing fixtures: %v", err)
		}
	}

	items, err := eval.LoadGolden(*golden)
	if err != nil {
		log.Fatalf("golden: %v", err)
	}

	// Ranking: embed the query, search the store, return MCP IDs best-first.
	rank := func(ctx context.Context, query string, topK int) ([]string, error) {
		vecs, err := emb.Embed(ctx, []string{query})
		if err != nil {
			return nil, err
		}
		hits, err := st.Search(ctx, vecs[0], topK, store.Filter{})
		if err != nil {
			return nil, err
		}
		ids := make([]string, len(hits))
		for i, h := range hits {
			ids[i] = h.MCP.ID
		}
		return ids, nil
	}

	rep, err := eval.Run(ctx, items, rank, *k)
	if err != nil {
		log.Fatalf("eval: %v", err)
	}

	fmt.Printf("\n=== Zeus search quality (enricher=%s, embedder=%s, collection=%s) ===\n", cfg.Enricher, emb.Name(), cfg.QdrantCollection)
	fmt.Println(rep.String())

	if *showFails {
		fails := make([]eval.QueryResult, 0)
		for _, q := range rep.PerQuery {
			if !q.HitAtK {
				fails = append(fails, q)
			}
		}
		sort.Slice(fails, func(i, j int) bool { return fails[i].Query < fails[j].Query })
		if len(fails) > 0 {
			fmt.Printf("\nMisses (%d):\n", len(fails))
			for _, q := range fails {
				fmt.Printf("  - %q\n      expected %v\n      got      %v\n", q.Query, q.Expected, top(q.Returned, 3))
			}
		}
	}
	fmt.Println()
}

func top(ss []string, n int) []string {
	if len(ss) > n {
		return ss[:n]
	}
	return ss
}
