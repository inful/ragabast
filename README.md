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
  "query":       "kubernetes ingress tls",
  "limit":       5,                       // default 5
  "min_score":   0,                       // optional, default 0 (no floor)
  "mode":        "hybrid",                // hybrid | semantic | keyword
  "document_id": "",                      // optional filter
  "tag":         "security",              // optional filter
  "category":    "tutorial"               // optional filter
}
```

`mode` selects the ranking strategy. The v0.4.0 default is `hybrid`;
pass `semantic` for pure embedding similarity (the v0.3.0 behaviour) or
`keyword` for bleve-only search. Unknown modes return 422.

Filters are applied to BOTH rankings before fusion; a chunk that fails
the `document_id` / `tag` / `category` filter never appears in the
result, even if it ranks at the top of both lists.

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
`server.auth_token` is non-empty. Clients send `Authorization: Bearer <token>`;
the token is compared in constant time (`crypto/subtle.ConstantTimeCompare`)
so the endpoint cannot be used as a timing oracle.

Wire format:
```bash
curl -H 'Authorization: Bearer YOUR_TOKEN' http://localhost:8080/api/health
```

Public routes (always open, even when auth is configured) — these are the
GET pages that load the HTML chrome in the operator's browser, plus static
assets and CORS preflight:

| Method | Path |
|---|---|
| GET | `/`, `/chat`, `/search`, `/ingest`, `/documents` |
| GET | `/static/*` |
| OPTIONS | `*` |

Default behaviour when `auth_token` is empty: open access. This keeps the
local single-user install working without configuration. Operators exposing
ragabast on a non-loopback interface **must** set this — leaving it empty
means anyone who can reach the listener can ingest, query, and delete
documents.

Env: `SERVER_AUTH_TOKEN`.

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
| `GET` | `/api/ingest/jobs/{job_id}` | Poll job status. Returns `pending`, `processing`, `completed`, or `failed` plus `document_id`, `chunks`, and timing fields. |

The synchronous `POST /api/ingest` path remains available for
interactive use — both endpoints share the same auth, rate limit, and
per-document size cap.

| Field | Default | Effect |
|---|---|---|
| `server.async_ingest_queue_dir` | `data/jobs` | On-disk directory for persisted jobs. Empty disables async ingest (endpoints return 503). Directory is `0700`; each job file is `0600`. Env: `SERVER_ASYNC_INGEST_QUEUE_DIR`. |
| `server.async_ingest_workers` | `5` | Worker-pool concurrency. `0` falls back to `1`. Env: `SERVER_ASYNC_INGEST_WORKERS`. |

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
audit pipeline.### HTTP hardening (H-1, H-2, M-4)

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
