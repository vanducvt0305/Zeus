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

Three design choices make matching sharper:

- **Enrichment / capability cards (the biggest lever).** Search quality is
  bounded by how well each MCP is *represented*. Before indexing, every MCP is
  rewritten into a capability card: a normalized summary, the tasks it does in
  agent-intent language, **synthetic example queries**, synonyms, and
  categories. This closes the gap between how agents *ask* ("look up information
  online") and how MCPs *describe themselves* ("Neural web search engine").
- **Multi-representation indexing.** Each MCP is embedded at the *server* level,
  the *tool* level (one vector per tool), and the *query* level (one vector per
  synthetic example query). Agent queries are usually tool/task-shaped, so the
  finer-grained vectors match more precisely. Search collapses results back to
  one entry per MCP, keeping the best score.
- **Pluggable everything.** The indexer and server share `Embedder`, `Enricher`,
  and `Store` interfaces, so the embedding model, enrichment strategy, and
  vector backend are config switches, not code changes.

## Project layout

```
cmd/
  server/      MCP discovery server (stdio) — what agents connect to
  indexer/     CLI that crawls a source and populates the vector store
  eval/        CLI that scores search quality against a golden set
internal/
  model/       normalized MCP schema + capability card (one shape for every source)
  source/      catalogs of MCPs (Source interface + official registry + file)
  enrich/      Enricher interface + heuristic (offline) + LLM capability cards
  llm/         Client interface + Anthropic + OpenAI-compatible chat
  embed/       Embedder interface + hash (offline) + OpenAI-compatible impls
  store/       Store interface + Qdrant implementation (server/tool/query points)
  index/       indexer: source → enrich → embed → store
  server/      the three MCP tools
  eval/        IR metrics (Hit@1, Recall@k, MRR, nDCG@k) + runner
  config/      env-driven config; builds enricher + embedder + store
eval/
  fixtures/    a fixed MCP catalog for reproducible evaluation
  golden.json  labeled (query → expected MCP) pairs
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

## Enrichment (capability cards)

Enrichment is the highest-leverage stage. Choose it with `ENRICHER`:

| `ENRICHER` | What it does | Needs |
|---|---|---|
| `heuristic` (default) | Offline; derives tasks and example queries by humanizing tool names and description phrases. | nothing |
| `llm` | Rewrites each MCP into a rich capability card with real agent-language synthetic queries. | an LLM (`LLM_API_KEY`) |
| `none` | No enrichment — the baseline for measuring the others. | nothing |

The LLM enricher works with Claude (`LLM_PROVIDER=anthropic`) or any
OpenAI-compatible chat endpoint (`LLM_PROVIDER=openai`, including a local
Ollama). It falls back to the heuristic enricher per-record on any failure, so a
flaky model never stalls the pipeline.

```bash
export ENRICHER=llm
export LLM_PROVIDER=anthropic
export LLM_API_KEY=sk-ant-...
export LLM_MODEL=claude-haiku-4-5   # fast + cheap is ideal for this batch job
make index LIMIT=200
```

## Evaluation

You can't improve what you don't measure. `cmd/eval` runs a golden set of
`(query → expected MCP)` pairs against the index and reports **Hit@1,
Recall@k, MRR, nDCG@k**. It uses a fixed fixture catalog
([`eval/fixtures/mcps.json`](./eval/fixtures/mcps.json)) so results are
reproducible and don't drift with the live registry.

```bash
make qdrant-up
make eval            # index fixtures + score, with the misses listed

# Quantify the effect of enrichment — each config in its own collection:
make eval-compare
```

The harness is the point: every change to enrichment, embedding, or retrieval
should be judged by these numbers, not by eyeballing a few queries.

> Note on the offline defaults: with the lexical `hash` embedder, enrichment is
> roughly neutral (well-named tools already carry the lexical signal, and extra
> query points can collide). Its real payoff appears with a **semantic** embedder
> — there, the LLM-generated task language and synthetic queries bridge the
> vocabulary gap that lexical matching can't. The harness lets you confirm this
> on your own embedder.

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

Implemented: official-registry + file sources, **enrichment pipeline (heuristic
+ LLM capability cards with synthetic queries)**, multi-representation indexing
(server/tool/query), Qdrant store, the three discovery tools, hash +
OpenAI-compatible embedders, and an **evaluation harness** with a golden set.

Natural next steps:

- **More sources.** A GitHub crawler (`topic:mcp`, parse `server.json`/README)
  and aggregators (mcp.so, Smithery, Glama) — just add a `source.Source`.
- **Tool extraction.** The registry describes how to *connect to* servers but
  not their tools; connect to each server and call `tools/list` to populate real
  tool- and query-level vectors (today tools come only from sources that list
  them, like the fixtures).
- **Hybrid search.** Combine dense vectors with sparse/keyword (Qdrant supports
  both) so exact tool-name matches aren't missed.
- **Cross-encoder re-ranker.** Re-score the top-K with a model that reads query
  and candidate together — the next big precision lever after enrichment.
- **Online feedback loop.** Log which MCP an agent actually selected and whether
  the task succeeded, and feed those labels back into ranking.

## Development

```bash
make build   # build both binaries into ./bin
make test    # run unit tests
go vet ./...
```
