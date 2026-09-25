# ragabast

docbuilder markdown chunker and rag/llm information retriever

## Configuration

ragabast reads configuration from YAML.

Precedence:
- `--config <path>` (per-command)
- `RAGABAST_CONFIG` env var (equivalent to `--config`)
- `./config.yml`
- `./config.yaml`
- built-in defaults (with env var overrides like `OLLAMA_BASE_URL`)

See [config.example.yml](config.example.yml) for a starting point.

### Environment variable reference

Every env var ragabast recognises, with its YAML key (where
applicable), built-in default, and a one-line description. Useful
for operators scripting a deploy who don't want to read the full
YAML to find the knob they need.

#### `ollama` — LLM provider

| Env var | YAML key | Default | Description |
|---|---|---|---|
| `OLLAMA_BASE_URL` | `base_url` | `http://localhost:11434` | Embeddings server URL. Strip trailing `/v1` if present. |
| `OLLAMA_CHAT_BASE_URL` | `chat_base_url` | `http://localhost:11434` | Chat completions server URL. Same provider family as embeddings, often the same host. |
| `OLLAMA_CHAT_MODEL` | `chat_model` | `gemma:2b` | Model name to send in `/v1/chat/completions` requests. |
| `OLLAMA_EMBEDDING_MODEL` | `embedding_model` | `nomic-embed-text:v1.5` | Model name to send in `/v1/embeddings` requests. |
| `OLLAMA_EMBEDDING_DIMENSIONS` | `embedding_dimensions` | `0` (off) | Matryoshka truncation request — `jina v5`, OpenAI `text-embedding-3-*`. 0 disables. Must match `vectordb.embedding_dimension`. |
| `OLLAMA_EMBEDDING_CONCURRENCY` | `embedding_concurrency` | `4` | Concurrent `/v1/embeddings` requests in flight. |
| `OLLAMA_EMBEDDING_DOC_PROMPT` | `embedding_doc_prompt` | `""` | Task-name prefix for chunks being indexed. Leave empty for models without task input. |
| `OLLAMA_EMBEDDING_QUERY_PROMPT` | `embedding_query_prompt` | `""` | Task-name prefix for user queries. Must differ from `_DOC_PROMPT` for asymmetric retrieval. |
| `OLLAMA_API_KEY` | `api_key` | `""` | Bearer token for both servers. Empty for unauthenticated local. |
| `OLLAMA_CHAT_API_KEY` | `chat_api_key` | `""` | Chat-specific bearer token. Wins over `api_key` for chat only. |
| `OLLAMA_EMBEDDING_API_KEY` | `embedding_api_key` | `""` | Embeddings-specific bearer token. Wins over `api_key` for embeddings only. |
| `OLLAMA_TEMPERATURE` | `temperature` | `0.1` | LLM sampling temperature. Pointer field — unset in env keeps the YAML default. |
| `OLLAMA_OPTIONS_JSON` | `options` | `{top_k:20, top_p:0.8, min_p:0.05, num_predict:512}` | Pass-through OpenAI-compat sampling options. JSON-encoded in env. |
| `OLLAMA_TIMEOUT` | `timeout` | `30s` | Per-request timeout for both embeddings and chat. |

#### `vectordb` — vector store

| Env var | YAML key | Default | Description |
|---|---|---|---|
| `VECTOR_DB_DIR` | `persistence_dir` | `<cwd>/data/vectors` | On-disk chromem-go directory. Changing requires re-ingest. |
| `VECTOR_DB_KEYWORD_INDEX_DIR` | `keyword_index_dir` | `""` (→ `<persistence_dir>/search`) | bleve keyword index path. Override when persistence is on NFS. |
| `VECTOR_DB_COLLECTION` | `collection_name` | `ragabast` | chromem-go collection name. |
| `VECTOR_DB_DIMENSION` | `embedding_dimension` | `768` | Embedding vector size. Must match the truncated dimension if `OLLAMA_EMBEDDING_DIMENSIONS > 0`. |

#### `server` — HTTP layer

| Env var | YAML key | Default | Description |
|---|---|---|---|
| `SERVER_ADDRESS` | `address` | `0.0.0.0` | Bind address. Use `127.0.0.1` for local-only. |
| `SERVER_PORT` | `port` | `8080` | Listen port. |
| `SERVER_ENABLE_CORS` | `enable_cors` | `true` | Master switch for CORS processing. |
| `SERVER_CORS_ORIGINS` | `cors_origins` | `[]` | Allowed origins (comma-separated). `*` for trusted local-only. |
| `SERVER_AUTH_TOKEN` | `auth_token` | `""` | Single bearer token. Empty = open access (local-dev default). |
| `SERVER_AUTH_TOKENS` | `auth_tokens` | `[]` | Array of `{label, value}` bearer tokens. Mix with `SERVER_AUTH_TOKEN` is undefined. |
| `SERVER_RATE_LIMIT_PER_MINUTE` | `rate_limit_per_minute` | `0` (off) | Sustained per-IP rate. `0` disables the limiter. |
| `SERVER_RATE_LIMIT_BURST` | `rate_limit_burst` | `5` | Immediate requests allowed before the per-minute rate kicks in. |
| `SERVER_MAX_INGEST_DOCUMENT_BYTES` | `max_ingest_document_bytes` | `1048576` (1 MiB) | Per-document size cap for both sync and async ingest paths. |
| `SERVER_READ_TIMEOUT` | `read_timeout` | `15s` | `http.Server.ReadTimeout`. |
| `SERVER_WRITE_TIMEOUT` | `write_timeout` | `15s` | `http.Server.WriteTimeout`. |
| `SERVER_MCP_HTTP_ENABLED` | `mcp_http_enabled` | `false` | Mount the MCP streamable-HTTP server at `/mcp`. Off by default. |
| `SERVER_ASYNC_INGEST_QUEUE_DIR` | `async_ingest_queue_dir` | `data/jobs` | On-disk async job queue. Empty disables async ingest (endpoints 503). |
| `SERVER_ASYNC_INGEST_WORKERS` | `async_ingest_workers` | `5` | Worker pool concurrency. `0` falls back to `1`. |
| `SERVER_ASYNC_INGEST_CLEANUP_INTERVAL` | `async_ingest_cleanup_interval` | `1h` | Background sweeper cadence for evicting finished jobs past TTL. |
| `SERVER_ASYNC_INGEST_COMPLETED_JOB_TTL` | `async_ingest_completed_job_ttl` | `168h` (7 days) | How long completed jobs are retained for `/api/ingest/jobs/{id}` history. |
| `SERVER_ASYNC_INGEST_FAILED_JOB_TTL` | `async_ingest_failed_job_ttl` | `720h` (30 days) | How long failed jobs are retained. |

#### `processing` — chunking

| Env var | YAML key | Default | Description |
|---|---|---|---|
| `PROCESSING_MAX_CHUNK_SIZE` | `max_chunk_size` | `2000` | Maximum characters per chunk. |
| `PROCESSING_MIN_CHUNK_SIZE` | `min_chunk_size` | `300` | Minimum characters per chunk. |
| `PROCESSING_CHUNK_OVERLAP` | `chunk_overlap` | `150` | Character overlap between consecutive chunks. |

#### `paths` — filesystem layout

| Env var | YAML key | Default | Description |
|---|---|---|---|
| `DATA_DIR` | `data_dir` | `<cwd>/data` | Root for all on-disk data. |
| `TEMPLATES_DIR` | `templates_dir` | `""` (→ embedded templates) | Override the HTML template set without rebuilding. |

#### `ragabast` — presentation / linking / caching

| Env var | YAML key | Default | Description |
|---|---|---|---|
| `RAGABAST_DOCBUILDER_BASE_URL` | `docbuilder_base_url` | `""` | docbuilder root — every search result gets a synthetic `<base>/_uid/<uid>/` permalink. |
| `RAGABAST_LOG_CHAT_REQUESTS` | `log_chat_requests` | `false` | Log every chat-completions request/response under `[chat-debug]` on stderr. |
| `RAGABAST_QUERY_CACHE_SIZE` | `query_cache_size` | `512` | LRU cap for the search cache. `0` disables. |
| `RAGABAST_QUERY_CACHE_TTL` | `query_cache_ttl` | `5m` | Per-entry TTL. |
| `RAGABAST_CHAT_SESSION_MAX_TURNS` | `chat_session_max_turns` | `20` | FIFO-trimmed cap on the in-memory chat session store. Each turn is user + assistant. |

Notes:
- Set `ollama.temperature` in YAML (or `OLLAMA_TEMPERATURE`) to control sampling.
- `ragabast query --temperature ...` overrides config/env for that invocation.
- The chat completions server (`ollama.chat_base_url` / `ollama.chat_model`) speaks
  the OpenAI Chat Completions API. Works against Ollama 0.5+, vLLM, llama.cpp
  `--server`, LM Studio, llama-stack, OpenRouter, and OpenAI itself.
- The embeddings server (`ollama.base_url` / `ollama.embedding_model`) speaks
  the OpenAI Embeddings API. Works against Ollama 0.5+ (with the
  `nomic-embed-text` image), vLLM, llama.cpp `--embedding`, LM Studio, OpenAI,
  and Google's Generative AI API via its OpenAI-compat layer
  (`https://generativelanguage.googleapis.com/v1beta/openai`). Use the actual
  Google embedding model names: `gemini-embedding-001` (text, 768 dims) or
  `gemini-embedding-2` (multimodal, 3072 dims by default; the latest). The
  bearer-token auth flow is identical to OpenAI's, so `ollama.embedding_api_key`
  just takes your `GEMINI_API_KEY`.
- Use `ollama.api_key` as the default bearer token for both servers. Set
  `ollama.chat_api_key` and/or `ollama.embedding_api_key` (env:
  `OLLAMA_CHAT_API_KEY`, `OLLAMA_EMBEDDING_API_KEY`) when the chat and
  embeddings providers require different tokens.
- Changing `embedding_model` (or its `EmbeddingDimension` in `vectordb:`) requires
  re-ingesting all documents: stop the server, `rm -rf data/vectors/`, and run
  `ragabast ingest` again.
- Set `ragabast.log_chat_requests: true` (or env
  `RAGABAST_LOG_CHAT_REQUESTS=true`) to have the chat-completions
  client log every request and response body under the
  `[chat-debug]` log prefix on stderr. Use this to verify the
  system prompt actually reaches the model — you'll see the full
  conversation including the parts ragabast's post-processors
  strip from the user-visible reply. Default false. Bodies over
  8 KiB are truncated with a `...[truncated]` marker. The bearer
  token is never logged.
- Set `ragabast.docbuilder_base_url` in YAML (or
  `RAGABAST_DOCBUILDER_BASE_URL`, e.g.
  `https://docs.example.com`) to surface a synthetic
  permalink for every search result. ragabast computes
  `<base>/_uid/<uid>/` from each document's UID frontmatter
  and exposes it as `docbuilder_url` in API responses, the
  link-suggestions endpoint, the web UI, and the chat prompt
  so the LLM can cite a stable direct link. The `/_uid/` alias
  is docbuilder's stable permalink convention: derived from
  the frontmatter UID rather than the file path, so downstream
  indexers, bookmarks, and external links stay valid even
  after the doc moves on disk. Empty by default, which means
  only the doc's own frontmatter `urls:` surface as links.
- Set `ollama.embedding_dimensions` in YAML (or `OLLAMA_EMBEDDING_DIMENSIONS`)
  to request Matryoshka truncation from the embeddings server. Useful for
  `jina-embeddings-v5-text-small` (supported: 32, 64, 128, 256, 512, 768,
  1024) and OpenAI `text-embedding-3-*` (any positive integer). Leave at 0 to
  disable truncation; the field is omitted from the request body when unset so
  older Ollama versions don't reject it. When set, the client logs a one-shot
  `DIMENSION MISMATCH` warning if the server returns vectors of a different
  length than configured. **Not supported by Google Gemini** — the
  OpenAI-compat layer silently ignores the field; if you set
  `embedding_dimensions: N > 0` against Google you'll get a
  `DIMENSION MISMATCH` warning on every request. Leave at 0. To get a
  different dimension out of Google's `gemini-embedding-2` model, use the
  native endpoint (`/v1beta/models/gemini-embedding-2:embedContent`) with
  the `outputDimensionality` request field — not currently wired through
  ragabast.

## Usage

- Start the web server: `ragabast serve`
- Use a specific config file: `ragabast serve --config ./config.yml`
- Create a starter config: `ragabast config init` (or `ragabast init`) (writes `config.yml` with mode `0600`)

## Search

ragabast exposes a hybrid search endpoint that combines an embedding
similarity ranking with a BM25 keyword ranking, fused via Reciprocal
Rank Fusion (RRF, k=60).

```
POST /api/search
{
  "query":          "kubernetes ingress tls",
  "limit":          5,                       // default 5
  "min_score":      0,                       // optional, default 0 (no floor)
  "mode":           "hybrid",                // hybrid | semantic | keyword
  "document_id":    "",                      // optional filter
  "tag":            "security",              // optional filter
  "category":       "tutorial",              // optional filter
  "created_after":  "2026-01-01T00:00:00Z", // optional, RFC3339
  "created_before": "2026-12-31T23:59:59Z", // optional, RFC3339
  "updated_after":  "",                      // optional, RFC3339
  "updated_before": ""                       // optional, RFC3339
}
```

`mode` selects the ranking strategy. The v0.4.0 default is `hybrid`;
pass `semantic` for pure embedding similarity (the v0.3.0 behaviour) or
`keyword` for bleve-only search. Unknown modes return 422.

Filters are applied to BOTH rankings before fusion; a chunk that fails
the `document_id` / `tag` / `category` filter never appears in the
result, even if it ranks at the top of both lists. Date filters
(`created_after` / `created_before` / `updated_after` / `updated_before`)
constrain by the parent document's frontmatter `created` /
`updated` timestamps, and combine with AND semantics — the document
must fall in every specified range.

CLI equivalent:

```
ragabast search "kubernetes ingress tls" \
  --tag security \
  --mode hybrid
```

The keyword index lives at `<vectordb.persistence_dir>/search/` next to
the vector DB. `ragabast vector reset --force` wipes both stores in one
go; the next `ragabast ingest` rebuilds them from source documents.

> **Filesystem note:** both stores assume a local filesystem with
> sub-second clock skew. bleve uses mmap for reads, which performs
> poorly or fails outright on NFS — and `jobs.Queue` uses a 5-second
> mtime threshold to fence live workers from crashed ones, which NFS
> clock drift can defeat. If `vectordb.persistence_dir` is on a shared
> mount, set `vectordb.keyword_index_dir` (env `VECTOR_DB_KEYWORD_INDEX_DIR`)
> to a local SSD so the bleve index can mmap safely while the vector DB
> stays on the shared volume.

### Why hybrid

Pure embedding search paraphrases ("how do I configure TLS" matches
`tls_handshake: configure`) but misses exact terms. Pure keyword
search gets exact-term recall right but cannot paraphrase. RRF fuses
the two rankings so a chunk that ranks #1 in either list is surfaced,
and a chunk that ranks highly in both rises to the top.

## Security

ragabast ships hardened for safe local-dev use and for non-loopback
deployment. The defaults are safe-by-construction; every knob below has a
sensible value and is documented in [config.example.yml](config.example.yml).

### Authentication (C-1, H-3)

The web server's API and form-mounted endpoints require a bearer token when
`server.auth_token` (or `server.auth_tokens`) is non-empty. Clients send
`Authorization: Bearer <token>`; the token is compared in constant time
(`crypto/subtle.ConstantTimeCompare`) so the endpoint cannot be used as a
timing oracle.

Wire format:
```bash
curl -H 'Authorization: Bearer YOUR_TOKEN' http://localhost:8080/api/health
```

Multiple tokens with optional labels — use this when several operators
or services share one ragabast instance and you want audit logs to
distinguish them:

```yaml
server:
  auth_tokens:
    - label: alice        # optional, surfaces in access logs
      value: TOKEN_FOR_ALICE
    - label: docbuilder   # optional
      value: TOKEN_FOR_DOCBUILDER
```

Env: `SERVER_AUTH_TOKEN` (singular), `SERVER_AUTH_TOKENS` (comma-separated
list, no labels). Mixing the singular and plural env vars is undefined;
pick one.

Public routes (always open, even when auth is configured) — these are the
GET pages that load the HTML chrome in the operator's browser, plus static
assets and CORS preflight:

| Method | Path |
|---|---|
| GET | `/`, `/chat`, `/search`, `/ingest`, `/documents` |
| GET | `/static/*` |
| OPTIONS | `*` |

Default behaviour when no auth token is configured: open access. This
keeps the local single-user install working without configuration.
Operators exposing ragabast on a non-loopback interface **must** set
this — leaving it empty means anyone who can reach the listener can
ingest, query, and delete documents.

### CORS (C-2)

CORS is **off by default at the allow-list level**. Setting
`server.enable_cors: true` enables CORS processing, but cross-origin browser
requests are only allowed when the request's `Origin` header appears in
`server.cors_origins`. Wildcard `*` is still supported for trusted local-only
deployments but is not the default.

```yaml
server:
  enable_cors: true
  cors_origins:
    - https://docs.example.com
    - https://app.example.com
```

Env: `SERVER_CORS_ORIGINS` (comma-separated).

### Rate limiting (H-4)

The LLM-backed endpoints (`/api/query`, `/api/search`, `/api/link-suggestions`,
`/api/frontmatter/suggest`, `/chat/message`, `/search`) AND the ingest
endpoints (`/api/ingest`, `/api/ingest/raw`, `/api/ingest/file`, `/ingest`)
share a per-client-IP token-bucket limiter. The bucket refills
continuously; an empty bucket returns `429 Too Many Requests` with a
`Retry-After` header.

| Field | Default | Effect |
|---|---|---|
| `server.rate_limit_per_minute` | `0` (disabled) | Sustained per-IP rate. 0 disables the limiter. |
| `server.rate_limit_burst` | `5` | Immediate requests allowed before the per-minute rate kicks in. |

Health checks, static, and HTML form renders are NOT throttled.

Env: `SERVER_RATE_LIMIT_PER_MINUTE`, `SERVER_RATE_LIMIT_BURST`.

### Async ingest (high-volume docbuilder imports)

docbuilder imports documents in bursts — often thousands at once on a
fresh install. Synchronous ingest pins the HTTP client until each
embedding round-trip returns, which can time out long before the
import finishes. The async path lets docbuilder fire-and-forget.

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/api/ingest/async` | Submit a document for async ingest. Returns `202 Accepted` with a `job_id`. The job is persisted to disk; a bounded worker pool processes it in the background. |
| `GET` | `/api/ingest/jobs` | List jobs with optional `status`, `limit`, and `offset` query params. Returns the jobs array plus a `total` count for pagination. |
| `GET` | `/api/ingest/jobs/{job_id}` | Poll job status. Returns `pending`, `processing`, `completed`, or `failed` plus `document_id`, `chunks`, and timing fields. |

The synchronous `POST /api/ingest` path remains available for
interactive use — both endpoints share the same auth, rate limit, and
per-document size cap.

For very-large batch imports (`POST /api/ingest/batch`), see the
[Batch ingest](#batch-ingest) section — it accepts up to N
documents in a single request (or NDJSON stream) and reports
per-item results without failing the whole call on one bad
document.

| Field | Default | Effect |
|---|---|---|
| `server.async_ingest_queue_dir` | `data/jobs` | On-disk directory for persisted jobs. Empty disables async ingest (endpoints return 503). Directory is `0700`; each job file is `0600`. Env: `SERVER_ASYNC_INGEST_QUEUE_DIR`. |
| `server.async_ingest_workers` | `5` | Worker-pool concurrency. `0` falls back to `1`. Env: `SERVER_ASYNC_INGEST_WORKERS`. |
| `server.async_ingest_cleanup_interval` | `5m` | How often the background sweeper runs to evict finished jobs past their TTL. Env: `SERVER_ASYNC_INGEST_CLEANUP_INTERVAL`. |
| `server.async_ingest_completed_job_ttl` | `24h` | How long a completed job's metadata file is kept on disk for `/api/ingest/jobs/{id}` history. Env: `SERVER_ASYNC_INGEST_COMPLETED_JOB_TTL`. |
| `server.async_ingest_failed_job_ttl` | `168h` | How long a failed job's metadata file is kept — typically longer than completed because operators want to see what went wrong. Env: `SERVER_ASYNC_INGEST_FAILED_JOB_TTL`. |

**Restart safety.** Every state transition is persisted to
`<queue_dir>/<job_id>.json` (or `<job_id>.processing.json` while a
worker holds the job). On startup, any unfinished job is re-enqueued
and processed. A deploy, crash, or OOM kill during a long import
resumes from where the process died — the in-memory pending channel
is a transient cache; the on-disk representation is the source of
truth.

**Audit trail.** Every state transition logs a line via the standard
`log` package:

```
ingest job event=ingest.job.started job_id=... remote_addr=... bytes=...
ingest job event=ingest.job.completed job_id=... document_id=... chunks=...
ingest job event=ingest.job.failed job_id=... error=...
```

Tail the operator log to monitor the queue without needing a separate
audit pipeline.

### Batch ingest

For docbuilder imports that aren't quite at the queueing scale
(thousands of jobs) but still represent dozens to hundreds of
documents at once, `POST /api/ingest/batch` accepts an array of
documents in a single request and returns per-item results. One
oversized document doesn't fail the whole batch — that document
reports as a per-item error and the rest are processed normally.

```
POST /api/ingest/batch
[
  {"content": "---\nfingerprint: ...\nuid: doc-1\n---\nbody..."},
  {"content": "---\nfingerprint: ...\nuid: doc-2\n---\nbody..."}
]
```

NDJSON stream variant — `Content-Type: application/x-ndjson`, one
JSON document per line. Useful for very large batches that don't fit
in memory as a single JSON array.

Response shape:

```json
{
  "results": [
    {"document_id": "doc-1", "chunks": 12, "ok": true},
    {"ok": false, "error": "document too large (12 MiB > 10 MiB cap)", "index": 1}
  ],
  "summary": {"total": 2, "succeeded": 1, "failed": 1}
}
```

A 200 response means the request was processed; check `results[*].ok`
for per-item outcomes. The HTTP request counts as ONE call against
the rate limiter, regardless of how many documents were inside.

### CSRF protection (H-5)

Form-mounted POSTs (`/chat/message`, `/chat/clear`, `/search`,
`/ingest`) require a CSRF token issued as a `ragabast_csrf`
double-submit cookie. The form embeds the token in a hidden
`csrf_token` field, and the server compares the cookie and the form
field in constant time before the handler runs. A missing or
mismatched pair is `403 Forbidden`.

The `/api/*` JSON endpoints don't enforce CSRF — they require a
bearer token instead, and JSON POSTs are not auto-submitted by
browsers. The CSRF middleware only guards the form POSTs that a
browser can trigger without explicit JS.

Tokens are per-session, 32 random bytes hex-encoded, and rotated on
every `GET /` page load. There is no on-disk persistence — a fresh
page load gets a fresh token. This is intentionally simple: the
attacker model is a third-party page that tries to auto-submit a
form to ragabast, not a long-running session hijack.

### HTTP hardening (H-1, H-2, M-4)

Every response carries the defense headers below:

| Header | Value |
|---|---|
| `X-Content-Type-Options` | `nosniff` |
| `Referrer-Policy` | `no-referrer` |
| `X-Frame-Options` | `DENY` |
| `Content-Security-Policy` | `default-src 'self'; script-src 'self' https://unpkg.com; style-src 'self' https://cdn.jsdelivr.net; img-src 'self' data:; frame-ancestors 'none'; base-uri 'self'; form-action 'self'` |
| `Strict-Transport-Security` | `max-age=63072000; includeSubDomains` |

Every request is also bounded:

- Request body ≤ 10 MiB (enforced by `http.MaxBytesReader`; Huma endpoints use Huma's own cap).
- `ReadTimeout` / `WriteTimeout` from config; fixed `IdleTimeout = 120s`.
- Handler-level `Timeout(60s)` (chi) cancels the handler context.

### LLM reply sanitization (H-6)

The chat reply is rendered to HTML by `goldmark` + bluemonday UGCPolicy and
then re-sanitized by a final `sanitizeForChatHTML` pass immediately before
the `template.HTML` wrap. The double pass is defense in depth: a single bug
in the markdown pipeline, `InlineSourceLinks`, or bluemonday itself does
not become XSS through the chat UI.

### Secrets in config (M-1, M-2)

`ragabast config init` writes `config.yml` with mode `0o600` (owner read/write
only). `config.yml` may carry bearer tokens (`server.auth_token`) and LLM API
keys (`ollama.api_key`); the default Linux umask of `022` would otherwise
produce a world-readable file. SaveConfig also chmods an existing file
to `0o600` before overwriting it, so the `config init --force` path stays safe.

The access log redacts values for these query keys before chi's logger
formats the line: `query`, `message`, `text`, `content`, `document_id`,
`docbuilder_base_url`. Non-sensitive keys (e.g. `tag`, `category`) pass
through unchanged.

The bearer token is **never** logged, even when `ragabast.log_chat_requests`
is true. Only the chat request/response bodies are written under the
`[chat-debug]` prefix.

## Documents

`/api/documents` and the `/documents` page both paginate. Defaults
keep the page-load bounded for the 10k-corpus case (where the old
unpaginated endpoint timed out):

```
GET /api/documents?limit=25&offset=0
```

Response shape includes the items, the total count, and a `Link`
header with `rel="next"` / `rel="prev"` URLs when applicable — same
shape GitHub's REST API uses, so curl pipelines can follow pages
without parsing JSON.

```
Link: <.../api/documents?limit=25&offset=25>; rel="next", <.../api/documents?limit=25&offset=0>; rel="prev"
```

The default page size (25) and the upper clamp (1000) are
hard-coded in the handler — they're not currently config knobs.
Requests for `limit=0` get the default; requests for `limit > 1000`
are silently clamped to 1000 (the spec was "clamp, don't reject").

### Bulk metadata updates

For corpus-wide metadata edits (e.g. applying a new tag to 200
documents at once), `POST /api/documents/bulk-update` accepts an
array of `{document_id, patch}` pairs and returns per-item
results. A single bad patch doesn't fail the whole call — that
document reports an error and the rest proceed normally.

```
POST /api/documents/bulk-update
{
  "patches": [
    {"document_id": "doc-1", "patch": {"tags": ["security", "tls"]}},
    {"document_id": "doc-2", "patch": {"category": "Reference"}}
  ]
}
```

Response shape:

```json
{
  "results": [
    {"document_id": "doc-1", "ok": true, "applied_tags": ["security", "tls"]},
    {"document_id": "doc-2", "ok": true, "applied_category": "Reference"}
  ],
  "summary": {"total": 2, "succeeded": 2, "failed": 0}
}
```

The HTTP request counts as ONE call against the rate limiter,
regardless of how many patches are inside.

## Health

Two endpoints, both unauthenticated:

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/health` | Liveness — `200 OK` with `{"status": "ok"}` if the process is running. No dependencies checked. |
| `GET` | `/api/health/full` | Readiness — returns `200` with per-subsystem status, or `503` if anything critical is degraded. Includes the async-ingest queue depth, the query cache hit-rate, the chromem-go vector store health, and any background sweeper state. |

`/api/health/full` is meant for k8s readiness probes and
load-balancer health checks. The simple `/api/health` is the
process-alive signal — use that for liveness probes that should
NOT pull a node out of rotation just because chromem-go is briefly
rebuilding its index.

## Performance

### Query cache (H-7)

Repeated identical searches hit an in-memory LRU cache instead of
re-running the embedding + BM25 + RRF pipeline. The cache key is
`(query, limit, filters, model)` — change any of these and you
miss. Cache invalidation is automatic: ingest and delete evict
every entry whose filter could match the affected document.

| Field | Default | Effect |
|---|---|---|
| `ragabast.query_cache_size` | `512` | Maximum number of cached search responses. LRU eviction once full. Env: `RAGABAST_QUERY_CACHE_SIZE`. |
| `ragabast.query_cache_ttl` | `5m` | Time-to-live per cache entry. Even on a quiet cache, every entry expires after this duration so model-output drift can't keep stale results alive forever. Env: `RAGABAST_QUERY_CACHE_TTL`. |

Hit rate is visible in `/api/health/full` — useful when tuning the
TTL and size knobs for your traffic shape. Set `query_cache_size: 0`
to disable the cache entirely (every search re-runs the full
pipeline).

## Chat history persistence

Follow-up questions in the chat UI used to be one-shot — the LLM
had no memory of the prior exchange, so "tell me more about that"
always returned a generic answer. The chat session store fixes
this: every conversation is keyed by a server-generated UUID held
in a `ragabast_chat_session` cookie, and prior turns are threaded
into subsequent LLM calls.

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/chat/export?session_id=<id>` | Stream the chat transcript as a markdown download (`text/markdown`, `Content-Disposition: attachment; filename="ragabast-chat-<id8>-<unix>.md"`). 404 when the session has no messages. |
| `POST` | `/chat/clear` | Drop the server-side history AND the browser cookie so the next chat starts fresh. The "private mode" toggle. |

The transcript format:

```markdown
## User

What is ragabast?

## Assistant

A markdown chunker and RAG server.
```

Sessions are in-memory only by default — restart drops them. The
chat session store does not persist to disk; see issue #77
(tracked) for the sqlite-backed follow-up.

| Field | Default | Effect |
|---|---|---|
| `ragabast.chat_session_max_turns` | `20` | FIFO-trimmed cap. Each turn is user + assistant (≈40 messages). Env: `RAGABAST_CHAT_SESSION_MAX_TURNS`. |

`/chat/clear` is the privacy affordance — operators who want a clean
slate tap it and the prior context is gone immediately, both in
the store and in the browser cookie.

## Embedding task prompts

```yaml
ollama:
  embedding_doc_prompt:    "search_document: "    # prepended to chunk text
  embedding_query_prompt:  "search_query: "       # prepended to user query
```

Env: `OLLAMA_EMBEDDING_DOC_PROMPT`, `OLLAMA_EMBEDDING_QUERY_PROMPT`.
Empty (the default) sends no prompt prefix — appropriate for
`nomic-embed-text`, `mxbai-embed-large`, and most other models
that don't take a task input.

Setting both prompts to the SAME value (or leaving both empty)
makes queries and documents indistinguishable, which can degrade
retrieval quality by 5-15% on asymmetric search tasks. The two
fields are intentionally separate so operators can tune them
independently.

### Models that take a task input

The canonical case is Google's **embedding-gemma** — open-weights,
designed to run locally via Ollama / vLLM / llama.cpp / LM Studio.
It's trained to expect a task prefix in the input text; without
one, retrieval quality drops measurably. The canonical prefixes:

| Model | `embedding_doc_prompt` | `embedding_query_prompt` |
|---|---|---|
| `embeddinggemma` / `embedding-gemma` | `search_document: ` | `search_query: ` |
| OpenAI `text-embedding-3-*` | ` ` *(space)* | ` ` *(space)* |
| jina v5 | `task=retrieval.passage: ` | `task=retrieval.query: ` |

OpenAI's `text-embedding-3-*` family accepts a "task" via prompt
prefix too, but it's rarely useful in practice — set both prompts
to a single space if you want to keep the prefix-prepend on (for
model symmetry) without biasing the embedding.

The OpenAI-compat server receives the prefixed text in the standard
`/v1/embeddings` request — there's no separate `task` field on the
wire. The prefix-prepend happens at the application layer, before
the HTTP POST.

### Models that DO NOT take a task input

`nomic-embed-text`, `mxbai-embed-large`, `bge-*`, `e5-*`, and most
older embedding models expect raw text. Leave both prompts empty
(the default). Setting a prefix on a model that doesn't expect
one can degrade quality.

### Google's native Gemini API

`embedding-gemma` is also available via Google's native Gemini
endpoint, but that API is **not** OpenAI-compat. ragabast does
not speak the native Gemini embedding protocol — operators
wanting hosted embedding-gemma would need a proxy that exposes
`/v1/embeddings`. The same caveat applies to Google's other
embedding models (`gemini-embedding-001`, `gemini-embedding-2`),
which work today only because Google's OpenAI-compat shim
translates the wire format; the `dimensions` field is silently
ignored by that shim, so Matryoshka truncation has no effect
against Google-hosted embeddings.

## MCP (Model Context Protocol)

ragabast exposes its corpus as MCP tools so any compatible
AI agent (Claude Code, Claude Desktop, opencode, ...) can
register ragabast as a knowledge source. The HTTP transport
mounts at `/mcp` on the same port `ragabast serve` listens
on; bearer-token auth via the existing `server.auth_tokens`
applies. Opt-in via `server.mcp_http_enabled: true` (or env
`SERVER_MCP_HTTP_ENABLED=true`).

When `mcp_http_enabled: true`:

```
# Claude Desktop config (claude_desktop_config.json)
{
  "mcpServers": {
    "ragabast": {
      "url": "https://ragabast.internal/mcp",
      "headers": {
        "Authorization": "Bearer YOUR_TOKEN"
      }
    }
  }
}
```

Tools exposed:

| Tool | Purpose |
|---|---|
| `search` | Hybrid search with mode (hybrid/semantic/keyword) and filters (document_id, tag, category). Returns markdown-formatted results. |
| `query` | Chat-mode RAG. Stateless — MCP clients manage conversation context themselves. Returns LLM answer + sources. |
| `list_documents` | Paginated document list (limit/offset). |
| `get_document` | Single-document fetch with chunk count, tags, categories, and URLs. |

The HTTP transport is the primary integration point — central
ragabast installs become reachable by remote MCP clients without
additional deployment. `csrfMiddleware` already exempts
requests with an `Authorization` header, so bearer-auth MCP
clients skip CSRF (no browser session to protect).

For local operator workflows, the binary also exposes a
stdio transport:

```
$ ragabast mcp serve --stdio
```

No auth — the operator IS the client. Useful for piping
through `opencode` or a local Claude Desktop instance that
talks to a process instead of an HTTP endpoint.

### What this PR doesn't include (follow-ups)

- **Resources and prompts**: just tools for v1. Resources would
  let agents list the corpus without a query — nice but a 4-tool
  implementation is enough to demonstrate value.
- **Streaming responses**: MCP supports them. ragabast's
  responses are small (markdown), so streaming isn't critical.
- **MCP at a separate port**: same-port mounting is simpler;
  the auth boundary operators already have covers the threat
  model. If you need network isolation, reverse-proxy /mcp
  separately.
- **WebSocket transport**: not part of the MCP spec.
