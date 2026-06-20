.PHONY: build server indexer eval eval-compare qdrant-up qdrant-down index index-tools tidy test clean

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

tidy:
	go mod tidy

test:
	go test ./...

clean:
	rm -rf bin
