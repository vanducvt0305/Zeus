.PHONY: build server indexer qdrant-up qdrant-down index tidy test clean

build:
	go build -o bin/server ./cmd/server
	go build -o bin/indexer ./cmd/indexer

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

tidy:
	go mod tidy

test:
	go test ./...

clean:
	rm -rf bin
