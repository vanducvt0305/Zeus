# Zeus — MCP Discovery Server

[![CI](https://github.com/vanducvt0305/zeus/actions/workflows/ci.yml/badge.svg)](https://github.com/vanducvt0305/zeus/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](./LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25-00ADD8.svg)](https://go.dev)
[![MCP](https://img.shields.io/badge/Model_Context_Protocol-server-7C3AED.svg)](https://modelcontextprotocol.io)

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
┌──────────────────────────────────────────────────────────────────────┐
│  INDEXER  (cmd/indexer, run periodically)                              │
│  sources → resolve → extract tools → enrich → trust-score → embed      │
│  registry / GitHub / file  ──►  dedupe  ──►  Qdrant                    │
└──────────────────────────────────────────────────────────────────────┘
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
│    • call_mcp(mcp_id, tool, args)  ← router   │
│  query pipeline:                              │
│    embed → hybrid retrieve (dense+sparse,RRF) │
│         → rerank → trust-blend → top-k        │
└─────────────────────────────────────────────┘
```

Four design choices make matching sharper:

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
- **Hybrid retrieval + reranking.** Each point carries a dense (semantic) and a
  sparse (keyword) vector. Search fuses both with Reciprocal Rank Fusion — dense
  catches meaning, sparse catches exact tool names and technical terms — then a
  reranker re-scores the shortlist by reading each candidate jointly with the
  query. This is the classic retrieve-then-rerank pattern.
- **Pluggable everything.** The pieces share `Embedder`, `Enricher`, `Store`,
  and `Reranker` interfaces, so the embedding model, enrichment strategy, vector
  backend, and reranker are config switches, not code changes.

## Project layout

```
cmd/
  server/      MCP discovery server (stdio) — what agents connect to
  indexer/     CLI that crawls a source and populates the vector store
  eval/        CLI that scores search quality against a golden set
internal/
  model/       normalized MCP schema + capability card (one shape for every source)
  source/      catalogs of MCPs (registry + GitHub crawler + file + multi)
  resolve/     identity resolution: dedupe/merge the same MCP across sources
  extract/     Extractor interface + remote tools/list probing (real tools)
  enrich/      Enricher interface + heuristic (offline) + LLM capability cards
  trust/       Scorer interface + heuristic signals + LLM quality scoring
  llm/         Client interface + Anthropic + OpenAI-compatible chat
  embed/       Embedder interface + hash (offline) + OpenAI-compatible impls
  sparse/      sparse keyword encoder for hybrid search
  store/       Store interface + Qdrant impl (named dense+sparse, RRF fusion)
  rerank/      Reranker interface + lexical (offline) + LLM reranker
  search/      query pipeline: embed → hybrid retrieve → rerank → top-k
  proxy/       router: forward a tool call to a discovered MCP (call_mcp)
  index/       indexer: sources → resolve → extract → enrich → trust → embed → store
  server/      the three MCP tools
  eval/        IR metrics (Hit@1, Recall@k, MRR, nDCG@k) + runner
  config/      env-driven config; builds the whole pipeline
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

## Sources

The indexer pulls MCPs from a `source.Source`, selected with `SOURCE`:

| `SOURCE` | What it crawls |
|---|---|
| `registry` (default) | The official MCP registry (`registry.modelcontextprotocol.io`). |
| `github` | GitHub repository search by MCP topics; parses a root `server.json` when present, else builds from repo metadata (description, topics, homepage). Set `GITHUB_TOKEN` to raise the search rate limit; `GITHUB_QUERIES` overrides the default topic queries. |
| `file` | A local JSON catalog at `SOURCE_FILE` — for hand-declared MCPs or fixtures. |

`SOURCE` may also be a comma-separated list — e.g. `SOURCE=registry,github` — to
crawl several catalogs in one pass; the duplicates they produce are merged by
identity resolution (below).

```bash
make index                       # registry
make index-github                # GitHub (GITHUB_TOKEN=... recommended)
SOURCE=registry,github make index
```

Records from every source share the same normalized schema, so resolution,
extraction, enrichment, indexing, and search treat them identically.

## Identity resolution

The same server can surface in more than one source; indexing both would return
duplicate, half-complete results. After fetching, the indexer dedupes
(`internal/resolve`):

- **Identity is the canonical name** (the reverse-DNS id every source uses, e.g.
  `io.github.acme/search`). Two records with the same name are the same MCP.
- **Repository URL is deliberately *not* identity** — a monorepo hosts many
  distinct servers behind one repo, so keying on it would wrongly collapse them.
  It is only a fallback for records that have no name.
- **Merging** keeps the highest-priority source's scalar fields
  (`registry` > `github` > `file`), fills gaps from the others, and unions list
  fields (transports, packages, tools, categories). Every contributing source is
  recorded in `sources` for provenance.

So a server in both the registry (connection details) and the GitHub crawl
(repo, stars, topics) — or extracted tools from one and metadata from the other
— becomes a single, richer record.

## Tool extraction

The registry tells you how to *connect to* a server but not which tools it
exposes — yet tools are the most query-relevant signal. With `EXTRACT_TOOLS=true`
the indexer connects to each server's **remote** endpoint (streamable-http or
sse) as an MCP client, calls `tools/list`, and folds the real tools into the
record before enrichment and indexing.

```bash
make index-tools LIMIT=200      # or: EXTRACT_TOOLS=true ./bin/indexer
```

Properties:

- **Safety first.** Only remote HTTP(S) endpoints are contacted. Package-based
  (npm/pypi/oci) stdio servers are **never installed or executed** — that would
  run untrusted third-party code.
- **Best-effort + concurrent.** Servers are probed in parallel
  (`EXTRACT_CONCURRENCY`) with a per-attempt deadline (`EXTRACT_TIMEOUT`).
  Unreachable servers or ones requiring auth are skipped, never fatal.
- **Non-destructive.** Records that already carry tools (e.g. file fixtures) pass
  through untouched.

Off by default, since it is slow and many public servers gate `tools/list`
behind authentication.

### Authenticated extraction

To reach servers that require auth, supply credentials and they are attached as
HTTP headers on the probe:

- `EXTRACT_AUTH_TOKEN` — a global bearer token applied to every server.
- `EXTRACT_CREDENTIALS` — a JSON file of per-server headers, keyed by MCP id,
  endpoint host, or a `*.host` wildcard; the most specific match wins, falling
  back to the global token:

  ```json
  {
    "io.github.acme/search": {"Authorization": "Bearer ..."},
    "api.example.com":       {"X-API-Key": "..."},
    "*.corp.internal":       {"Authorization": "Bearer ..."}
  }
  ```

Resolution order per server: exact MCP id → exact host → `*.host` wildcard →
global token → anonymous.

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

## Trust & quality scoring

Relevance alone isn't enough: an abandoned or spammy server shouldn't outrank a
solid one just because its description matches the words. At index time each MCP
gets a 0..1 **trust prior** (`internal/trust`, `TRUST`):

- `heuristic` (default, offline): combines **popularity** (stars, log-scaled),
  **recency** (last update), and **completeness** (has a description, tools, a
  way to connect, categories).
- `llm`: adds a model's judgment of clarity/legitimacy and risk flags, averaged
  with the heuristic signals (falls back to heuristic on failure).
- `none`: no prior.

At search time the prior is blended into ranking by `TRUST_WEIGHT` (default
`0.15`):

```
final = (1 - TRUST_WEIGHT) * relevance + TRUST_WEIGHT * trust
```

`relevance` is rank-based, so a modest weight only reorders among
comparably-relevant results — trust breaks ties toward better servers without
overriding a clearly more relevant match. `TRUST_WEIGHT=0` disables it.

## Retrieval pipeline

At query time (`internal/search`):

1. **Embed** the query (dense) and, for hybrid, encode it as a sparse keyword
   vector.
2. **Retrieve** a candidate pool. With `HYBRID=true`, a dense prefetch and a
   sparse prefetch are fused with **Reciprocal Rank Fusion** in Qdrant; RRF
   combines by rank, so the two score scales need not be comparable.
3. **Rerank** the pool — the `lexical` reranker scores query-term coverage over
   each candidate's full capability text; the `llm` reranker asks a model to
   order the shortlist.
4. **Blend trust** — nudge the order by each result's stored trust prior
   (see above).
5. **Truncate** to `top_k`.

Sparse vectors are always stored, so `HYBRID` and `RERANKER` can be changed at
query time without re-indexing. Tune the shortlist size with `RERANK_POOL`.

## Evaluation

You can't improve what you don't measure. `cmd/eval` runs a golden set of
`(query → expected MCP)` pairs against the index and reports **Hit@1,
Recall@k, MRR, nDCG@k**. It uses a fixed fixture catalog
([`eval/fixtures/mcps.json`](./eval/fixtures/mcps.json)) so results are
reproducible and don't drift with the live registry.

```bash
make qdrant-up
make eval            # index fixtures + score, with the misses listed
make eval-compare    # ablation: dense-only → +hybrid → +hybrid+rerank
```

Ablation on the fixtures (offline `hash` embedder + heuristic enrichment), each
stage added on top of the previous:

| Retrieval | Hit@1 | Recall@5 | MRR | nDCG@5 |
|---|---|---|---|---|
| dense-only, no rerank | 0.739 | 0.913 | 0.812 | 0.837 |
| + hybrid (dense+sparse, RRF) | 0.870 | **1.000** | 0.920 | 0.940 |
| + lexical rerank | **0.913** | **1.000** | **0.949** | **0.962** |

Hybrid retrieval finds the right MCP in the top-5 for every query and lifts
precision-at-1 sharply (exact tool-name matches that the lexical embedder's
dense side blurs); reranking then cleans up the ordering. The harness is the
point: every change to enrichment, embedding, or retrieval should be judged by
these numbers, not by eyeballing a few queries.

> Note on enrichment under the offline `hash` embedder: it is roughly neutral
> there (well-named tools already carry the lexical signal). Its real payoff
> appears with a **semantic** embedder, where LLM-generated task language and
> synthetic queries bridge the vocabulary gap. Hybrid + rerank, by contrast,
> help even on the offline defaults, as the table shows.

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

Or run it from the prebuilt container (needs a reachable Qdrant):

```json
{
  "mcpServers": {
    "zeus-discovery": {
      "command": "docker",
      "args": ["run", "-i", "--rm", "-e", "QDRANT_HOST=host.docker.internal", "ghcr.io/vanducvt0305/zeus"]
    }
  }
}
```

## Router mode (`call_mcp`)

Discovery finds the right MCP, but the agent still has to *connect* to it — and
most hosts only allow statically-configured servers. `call_mcp` closes that gap:
the agent asks Zeus to run a tool on a discovered server, and **Zeus connects to
the target and forwards the call**. The agent needs only one connection — to
Zeus, the switchboard.

```jsonc
// agent → Zeus
call_mcp({ "mcp_id": "io.github.acme/search", "tool": "web_search",
           "arguments": { "query": "..." } })
// Zeus connects to acme/search's remote endpoint, calls web_search,
// and returns its result.
```

- Works for servers exposing a **remote** (http/sse) endpoint; package-only
  (stdio) servers return a clear "install locally" error (Zeus never runs
  untrusted code).
- Uses the same SSRF-guarded, credential-aware connection path as extraction.
- Disable with `PROXY_ENABLED=false` (then the tool isn't registered).

## Host it as a remote gateway

By default the server speaks MCP over **stdio** (the host launches the binary).
Set `TRANSPORT=http` to run it as a **hosted remote gateway** — agents then point
at a URL with zero install, and one connection gives them discovery
(`search_mcp`) plus routing (`call_mcp`) over the whole ecosystem.

```bash
TRANSPORT=http HTTP_ADDR=:8080 ./bin/server   # MCP at /, health at /healthz
```

Client config for a hosted instance:

```json
{ "mcpServers": { "zeus": { "url": "https://your-host.example/" } } }
```

## Configuration

All settings come from the environment; see [`.env.example`](./.env.example).
Defaults run end-to-end with `docker compose up -d` and no further setup.

## Status & roadmap

Implemented: registry + **GitHub crawler** + file sources (combinable via a
multi-source), **identity resolution** (dedupe/merge across sources), **live
tool extraction** (remote `tools/list` probing, with **per-server
authentication**), **enrichment pipeline (heuristic + LLM capability
cards with synthetic queries)**, **trust/quality scoring (heuristic + LLM)
blended into ranking**, multi-representation indexing
(server/tool/query), **hybrid retrieval (dense + sparse, RRF) with lexical/LLM
reranking**, Qdrant store, the discovery tools **plus a `call_mcp` router**
that forwards calls to discovered servers, hash + OpenAI-compatible embedders,
and an **evaluation harness** with a golden set and ablation.

Hardened for scale/ops: the full record is stored once per MCP (not on every
point) and search batch-fetches winners; payload field indexes; categories via
the facet API; deterministic point ids with stale-point pruning; durable
(`Wait`) writes; concurrent enrich/trust/GitHub stages; retry-with-backoff on
all outbound HTTP; an SSRF-guarded extractor (no private/loopback targets,
no cross-host credential leaks); and graceful shutdown.

Natural next steps:

- **Streaming indexer.** Process the source in batches (fetch → enrich → upsert
  per chunk) instead of holding the whole corpus in memory — needed for very
  large catalogs.
- **Vector quantization.** Enable Qdrant scalar quantization to cut memory at
  million-point scale.
- **More sources.** Aggregators (mcp.so, Smithery, Glama) — just add a
  `source.Source`.
- **OAuth extraction.** Static tokens/headers are supported; add the SDK's
  OAuth flow for servers that require interactive authorization.
- **Model-based cross-encoder.** The `Reranker` interface already supports it;
  add a hosted cross-encoder (e.g. a BGE reranker behind an HTTP endpoint).
- **IDF-weighted sparse.** The sparse encoder is stateless TF; persist corpus
  document-frequencies to upgrade it to BM25.
- **Online feedback loop.** Log which MCP an agent actually selected and whether
  the task succeeded, and feed those labels back into ranking.

## Publishing & listing

Agents and developers discover MCP servers through registries and directories,
not web search — so distribution means being listed where they look.

1. **Official MCP Registry.** [`server.json`](./server.json) describes this
   server (name `io.github.vanducvt0305/zeus`). Build and push the image, then
   publish:

   ```bash
   docker build -t ghcr.io/vanducvt0305/zeus:0.1.0 .
   docker push ghcr.io/vanducvt0305/zeus:0.1.0
   # https://github.com/modelcontextprotocol/registry — login with GitHub, then:
   mcp-publisher login github
   mcp-publisher publish
   ```

   The `io.github.<owner>/...` namespace is verified via your GitHub account.

2. **Aggregators / directories.** Submit to Smithery (uses
   [`smithery.yaml`](./smithery.yaml)), Glama, mcp.so, PulseMCP, and the
   `awesome-mcp-servers` GitHub list. Most of them crawl GitHub + `server.json`,
   so a clean repo carries you a long way.

3. **GitHub repo.** Add the topics `mcp`, `mcp-server`,
   `model-context-protocol`, `vector-search` (Settings → Topics) so the crawlers
   and `topic:` searches find it, and keep the README demo current.

## Development

```bash
make build   # build both binaries into ./bin
make test    # run unit tests
go vet ./...
```
