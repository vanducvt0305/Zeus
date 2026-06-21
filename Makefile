.PHONY: build server indexer eval eval-compare eval-semantic eval-semantic-enrichment qdrant-up qdrant-down index index-tools index-github tidy test clean

build:
	go build -o bin/server ./cmd/server
	go build -o bin/indexer ./cmd/indexer
	go build -o bin/eval ./cmd/eval

server: build
	./bin/server

indexer: build

# Start/stop local Qdrant.
qdrant-up:
	docker compose up -d

qdrant-down:
	docker compose down

# Index the official registry. Override LIMIT to cap how many servers are pulled,
# e.g. `make index LIMIT=100`.
LIMIT ?= 0
index: build
	./bin/indexer -limit $(LIMIT)

# Index the registry WITH live tool extraction (connects to remote servers and
# calls tools/list). Slower; many servers need auth.
index-tools: build
	EXTRACT_TOOLS=true ./bin/indexer -limit $(LIMIT)

# Crawl GitHub for MCP repositories instead of the registry. Set GITHUB_TOKEN to
# raise the search rate limit.
index-github: build
	SOURCE=github ./bin/indexer -limit $(LIMIT)

# Index the eval fixtures and print the search-quality scorecard.
eval: build
	./bin/eval -index -fails

# Ablation: index once (sparse vectors are always stored), then score the same
# collection with retrieval features toggled at query time. Requires Qdrant.
eval-compare: build
	@QDRANT_COLLECTION=eval_ablation ENRICHER=heuristic ./bin/eval -index >/dev/null
	@echo "1) dense-only, no rerank :"; QDRANT_COLLECTION=eval_ablation HYBRID=false RERANKER=none    ./bin/eval | grep -E "Hit@1|Recall|MRR|nDCG"
	@echo "2) +hybrid (dense+sparse):"; QDRANT_COLLECTION=eval_ablation HYBRID=true  RERANKER=none    ./bin/eval | grep -E "Hit@1|Recall|MRR|nDCG"
	@echo "3) +hybrid +rerank       :"; QDRANT_COLLECTION=eval_ablation HYBRID=true  RERANKER=lexical ./bin/eval | grep -E "Hit@1|Recall|MRR|nDCG"

# Semantic eval: score with a real embedding model instead of the offline hash
# embedder. The hash embedder is lexical-only, so enrichment's synthetic-query
# language, BM25's IDF, and semantic matching all look weaker than they are — this
# profile is where those gains actually show. Defaults to a local Ollama running
# nomic-embed-text; override EMBED_* for any OpenAI-compatible /embeddings
# endpoint (OpenAI, Voyage, TEI). Needs the embedder reachable AND Qdrant up.
EMBED_BASE_URL ?= http://localhost:11434/v1
EMBED_MODEL    ?= nomic-embed-text
EMBED_DIM      ?= 768
SEMANTIC_ENV = EMBEDDER=openai EMBED_BASE_URL=$(EMBED_BASE_URL) EMBED_MODEL=$(EMBED_MODEL) EMBED_DIM=$(EMBED_DIM)

eval-semantic: build
	@$(SEMANTIC_ENV) QDRANT_COLLECTION=eval_semantic ./bin/eval -index -fails

# Enrichment ablation under the semantic embedder — the comparison the offline
# hash embedder can't show (enrichment is ~neutral there). Enrichment happens at
# index time, so each row reindexes its own collection. Add a third row with
# ENRICHER=llm and LLM_* set to measure LLM capability cards.
eval-semantic-enrichment: build
	@$(SEMANTIC_ENV) ENRICHER=none      QDRANT_COLLECTION=eval_sem_none ./bin/eval -index >/dev/null 2>&1
	@$(SEMANTIC_ENV) ENRICHER=heuristic QDRANT_COLLECTION=eval_sem_heur ./bin/eval -index >/dev/null 2>&1
	@echo "no enrichment :"; $(SEMANTIC_ENV) QDRANT_COLLECTION=eval_sem_none ./bin/eval | grep -E "Hit@1|Recall|MRR|nDCG"
	@echo "+ heuristic   :"; $(SEMANTIC_ENV) QDRANT_COLLECTION=eval_sem_heur ./bin/eval | grep -E "Hit@1|Recall|MRR|nDCG"

tidy:
	go mod tidy

test:
	go test ./...

clean:
	rm -rf bin
