# Zeus — MCP Discovery Server

A "meta-MCP": an MCP server that helps agents **find the right MCP servers for a
task**. Instead of hard-coding which MCPs an agent can use, the agent asks Zeus
in natural language — *"I need to search product data"* — and gets back the most
relevant MCP servers, ranked by semantic similarity.

Zeus indexes other MCP servers (from the official MCP registry, and other
catalogs over time) into a vector database, and exposes a small set of tools an
agent can call to discover capabilities at runtime. Think of it as
**service discovery for the MCP ecosystem**.

## How it works

```
┌─────────────────────────────────────────────┐
│  INDEXER  (cmd/indexer, run periodically)     │
│  source → normalize → embed → upsert          │
│  Official MCP Registry  ──►  Qdrant           │
└─────────────────────────────────────────────┘
                     │
              ┌──────▼──────┐
              │   Qdrant    │   vectors + MCP metadata
              └──────▲──────┘
                     │
┌─────────────────────────────────────────────┐
│  MCP SERVER  (cmd/server, stdio)              │
│  tools an agent calls:                        │
│    • search_mcp(query, top_k, categories…)    │
│    • get_mcp_details(id)                       │
│    • list_categories()                         │
└─────────────────────────────────────────────┘
```

Two design choices make matching sharper:

- **Two-level indexing.** Each MCP is embedded at the *server* level
  (name + description + categories) and, when the source provides them, at the
  *tool* level (one vector per tool). Agent queries are usually tool-shaped
  ("search data", "send email"), so tool-level vectors match more precisely.
  Search collapses results back to one entry per MCP, keeping the best score.
- **Pluggable embedder.** The indexer and server share one `Embedder` interface,
  so the embedding model is a config switch, not a code change.

## Project layout

```
cmd/
  server/      MCP discovery server (stdio) — what agents connect to
  indexer/     CLI that crawls a source and populates the vector store
internal/
  model/       normalized MCP schema (one shape for every source)
  source/      catalogs of MCPs (Source interface + official registry client)
  embed/       Embedder interface + hash (offline) + OpenAI-compatible impls
  store/       Store interface + Qdrant implementation
  index/       indexer: source → embed → store
  server/      the three MCP tools
  config/      env-driven config; builds embedder + store
```

## Quick start (zero external services)

The defaults use a local Qdrant and an offline **hash** embedder, so you can run
the whole pipeline without any API keys or model downloads.

```bash
# 1. Start Qdrant
docker compose up -d

# 2. Index some MCPs from the official registry (cap to 200 for a fast first run)
make index LIMIT=200

# 3. Run the MCP server
make server
```

> The hash embedder is lexical only — good enough to see the system work, but
> not semantically strong. For real quality, switch to a proper embedding model
> (below).

## Real semantic quality

Set `EMBEDDER=openai` and point it at any OpenAI-compatible `/embeddings`
endpoint. The **same** values must be set for both the indexer and the server.

| Backend | `EMBED_BASE_URL` | `EMBED_MODEL` | `EMBED_DIM` |
|---|---|---|---|
| Ollama (local, free) | `http://localhost:11434/v1` | `nomic-embed-text` | `768` |
| OpenAI | `https://api.openai.com/v1` | `text-embedding-3-small` | `1536` |
| Voyage (Anthropic-recommended) | `https://api.voyageai.com/v1` | `voyage-3` | `1024` |

```bash
export EMBEDDER=openai
export EMBED_BASE_URL=http://localhost:11434/v1
export EMBED_MODEL=nomic-embed-text
export EMBED_DIM=768
make index LIMIT=200 && make server
```

> Changing the embedder changes the vector dimension. Recreate the collection
> when you switch: `docker compose down -v` (wipes Qdrant), then re-index. Or set
> a fresh `QDRANT_COLLECTION` name.

## Connecting an agent

`cmd/server` speaks MCP over stdio, so any MCP client can launch it. Example
client config (Claude Code / Claude Desktop style):

```json
{
  "mcpServers": {
    "zeus-discovery": {
      "command": "/absolute/path/to/zeus/bin/server",
      "env": { "QDRANT_HOST": "localhost", "QDRANT_PORT": "6334" }
    }
  }
}
```

The agent then calls `search_mcp` to discover other MCPs to use.

## Configuration

All settings come from the environment; see [`.env.example`](./.env.example).
Defaults run end-to-end with `docker compose up -d` and no further setup.

## Status & roadmap

Implemented: official-registry source, two-level indexing, Qdrant store, the
three discovery tools, hash + OpenAI-compatible embedders.

Natural next steps:

- **More sources.** A GitHub crawler (`topic:mcp`, parse `server.json`/README)
  and aggregators (mcp.so, Smithery, Glama) — just add a `source.Source`.
- **Tool-level enrichment.** The registry describes how to *connect to* servers
  but not their tools; connect to each server and call `tools/list`, or parse
  READMEs, to populate tool-level vectors.
- **Hybrid search.** Combine dense vectors with sparse/keyword (Qdrant supports
  both) so exact tool-name matches aren't missed.
- **Categories.** Derive/ingest categories to make the `categories` filter rich.

## Development

```bash
make build   # build both binaries into ./bin
make test    # run unit tests
go vet ./...
```
