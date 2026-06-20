.PHONY: build server indexer eval eval-compare qdrant-up qdrant-down index tidy test clean

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

# Index the eval fixtures and print the search-quality scorecard.
eval: build
	./bin/eval -index -fails

# Compare baseline (no enrichment) vs heuristic enrichment on the fixtures,
# each in its own collection. Requires Qdrant running.
eval-compare: build
	@echo "=== ENRICHER=none ===";      ENRICHER=none      QDRANT_COLLECTION=eval_none      ./bin/eval -index
	@echo "=== ENRICHER=heuristic ==="; ENRICHER=heuristic QDRANT_COLLECTION=eval_heuristic ./bin/eval -index

tidy:
	go mod tidy

test:
	go test ./...

clean:
	rm -rf bin
