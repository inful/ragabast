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
| `OLLAMA_OPTIONS_JSON` | `options` | `{top_k:20, top_p:0.8, num_predict:512}` | Pass-through sampling options merged into the top-level `/v1/chat/completions` request body. JSON-encoded in env. `min_p` is intentionally NOT in the defaults — it's not in OpenAI's spec and vLLM rejects it (HTTP 400) when speculative decoding is enabled. Operators on Ollama / llama.cpp / non-spec-decoding vLLM can opt in via `OLLAMA_OPTIONS_JSON='{"min_p":0.05, ...}'`. |
| `OLLAMA_TIMEOUT` | `timeout` | `30s` | Per-request timeout for both embeddings and chat. |

#### `embedding_provider` — which embeddings client to use

| Env var | YAML key | Default | Description |
|---|---|---|---|
| `EMBEDDING_PROVIDER` | `embedding_provider` | `""` (= `"openai"`) | Selects the embeddings client. `""` / `"openai"` — OpenAI-compat `/v1/embeddings`. `"embedding_gemma"` — EmbeddingGemma `/v2/embed`. |

#### `embedding_gemma` — EmbeddingGemma `/v2/embed` provider

| Env var | YAML key | Default | Description |
|---|---|---|---|
| `EMBEDDING_GEMMA_BASE_URL` | `base_url` | `""` (required when provider=`embedding_gemma`) | Server root. Path `/v2/embed` is appended by the client. |
| `EMBEDDING_GEMMA_TIMEOUT` | `timeout` | `30s` | Per-request HTTP timeout. |
| `EMBEDDING_GEMMA_DIMENSIONS` | `embedding_dimensions` | `0` (off) | Expected response dimension. Mismatch logs a one-shot WARNING. |
| `EMBEDDING_GEMMA_CONCURRENCY` | `embedding_concurrency` | `1` | Parallel `/v2/embed` requests in flight on the ingest path. |
| `EMBEDDING_GEMMA_DOC_PROMPT` | `embedding_doc_prompt` | `""` | Task-name prefix for chunks being indexed. Recommended for embedding-gemma: `title: none | text: `. |
| `EMBEDDING_GEMMA_QUERY_PROMPT` | `embedding_query_prompt` | `""` | Task-name prefix for user queries. Recommended for embedding-gemma: `task: search result | query: `. |

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
| `SERVER_PUBLIC_URL` | `public_url` | `""` (derived from `address`+`port`) | Override the externally-reachable origin used to build OAuth callback URLs. Set behind a reverse proxy (Traefik / nginx / cloud L7 LB) when `address` doesn't match the public hostname. Trailing slash is trimmed. |
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

#### `auth` — OAuth / OIDC login (browser users)

| Env var | YAML key | Default | Description |
|---|---|---|---|
| `AUTH_SESSION_TTL` | `session_ttl` | `12h` | Max age of a session cookie. Sliding renewal on every authenticated request. `0` disables expiry (sessions live until restart). |
| `AUTH_COOKIE_NAME` | `cookie_name` | `ragabast_session` | Session-cookie name. Set distinct names when running multiple ragabast instances behind the same host. |
| `AUTH_PROVIDERS_JSON` | `providers` | `[]` | JSON array of OAuth providers. See `plans/oauth.md` for the schema. |

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
| GET | `/auth/login`, `/auth/<provider>/login`, `/auth/<provider>/callback`, `/auth/me` |
| GET | `/static/*` |
| OPTIONS | `*` |

Default behaviour when no auth token is configured: open access. This
keeps the local single-user install working without configuration.
Operators exposing ragabast on a non-loopback interface **must** set
this — leaving it empty means anyone who can reach the listener can
ingest, query, and delete documents.

#### User login via OAuth / OIDC (web browser flow)

Bearer tokens are API-friendly but awkward for humans — every browser
user would have to copy a token into a header. Configure one or more
identity providers under `auth.providers` so the web UI shows a "Sign in
with …" chooser and the browser exchanges the IdP's code for a session
cookie. The bearer token path keeps working in parallel, so existing
API consumers don't need to change.

Supported providers:

| `type`    | Notes                                                                |
|-----------|----------------------------------------------------------------------|
| `github`  | github.com OAuth app.                                                |
| `gitlab`  | gitlab.com by default; self-hosted when `base_url` is set.            |
| `forgejo` | Any Forgejo/Gitea instance; `base_url` is required (e.g. codeberg.org). |
| `oidc`    | Generic OpenID Connect via `discovery_url`. Covers Keycloak, Authentik, Authelia, Auth0, Forgejo (with OIDC enabled), and ADFS Server 2019+ (with the OIDC app template). |

```yaml
auth:
  # Session cookie TTL (sliding renewal on every authenticated
  # request). Default 12h. Set to 0 to disable expiry.
  session_ttl: 12h
  cookie_name: ragabast_session    # default
  providers:
    - name: company-gitlab
      type: gitlab
      client_id: ...
      client_secret: ...
      base_url: https://gitlab.example.com   # omit for gitlab.com
      # scopes: override the per-type defaults below. The
      # defaults are tuned to work out of the box:
      #   type=gitlab:  read_user, profile, email
      #   type=github:  read:user, user:email
      #   type=forgejo: read:user, user:email
      #   type=oidc:    openid, profile, email
      # For GitLab, read_user is REQUIRED — without it,
      # /api/v4/user returns HTTP 403 regardless of token
      # validity. Don't drop it unless you've switched to
      # type=oidc with a discovery_url (which uses the
      # OIDC UserInfo claim flow instead of /api/v4/user).
      allowed_users:                          # optional whitelist
        - alice@example.com
        - bob@example.com
```

The IdP must be configured with the callback URL
`https://<your-ragabast>/auth/<name>/callback` and the matching scopes.
See [`plans/oauth.md`](plans/oauth.md) for per-provider setup recipes
(client registration, scopes, callback URLs).

#### Behind a reverse proxy (Traefik / nginx / cloud L7 LB)

When ragabast binds to a private address (e.g. `0.0.0.0` or a
k8s Service ClusterIP) but is exposed to the internet through a
reverse proxy, the bind address is not what the IdP sees as the
callback host. Set `server.public_url` (env `SERVER_PUBLIC_URL`)
to the externally-reachable origin so the OAuth callback URL
ragabast sends to the IdP matches what you registered at the
provider:

```yaml
server:
  address: 0.0.0.0       # bind address (private)
  port: 8080
  public_url: https://ragabast.example.com   # what the IdP sees
```

The callback URLs the IdP needs to whitelist become:

- `https://ragabast.example.com/auth/<provider-name>/callback` for every configured provider

```bash
# Equivalent env-var form:
SERVER_ADDRESS=0.0.0.0
SERVER_PORT=8080
SERVER_PUBLIC_URL=https://ragabast.example.com
```

`public_url` preserves the scheme, host, optional port, and
optional subpath exactly as written (a trailing slash is
trimmed to avoid `<URL>//auth/...`). When unset, ragabast
falls back to `http://<address>:<port>` — the historical
behavior for single-host / localhost installs.

Browser UX:

- `GET /auth/login` — chooser page (auto-redirects to the single
  provider when only one is configured). All rendered pages
  (`/`, `/ingest`, `/documents`, `/chat`) include a navbar at the
  top: when OAuth is configured and the user is signed in, the
  navbar shows `username (provider)` and a `Sign out` button; when
  not signed in, a `Sign in` link to `/auth/login?next=<current URL>`
  so the chooser can return the user to where they were going
  after auth completes; when OAuth is not configured, the navbar
  renders nothing (the historical single-user open-access mode).
- `GET /auth/<provider>/login` — starts the Authorization Code + PKCE
  flow; redirects to the IdP.
- `GET /auth/<provider>/callback` — verifies the state cookie,
  exchanges the code, mints a session cookie, redirects to `?next=`
  (or `/` when no `?next=` was set on the originating request).
- `POST /auth/logout` — destroys the session, clears the cookie,
  redirects to `/auth/login`. CSRF-protected (the form carries a
  `csrf_token` hidden field that the csrf middleware checks
  against the `ragabast_csrf` cookie).
- `GET /auth/me` — JSON user info for the UI's "signed in as"
  widget: `{"authenticated": true, "subject": "…", "username": "…",
  "email": "…", "name": "…", "provider": "…", "role": "user"}` or
  `{"authenticated": false}` when no session is present. Always
  returns 200 — the UI uses `authenticated` to choose its state
  without branching on status codes.

Browser protection: when OAuth is configured and a browser hits a
protected route without a session, the middleware redirects to
`/auth/login?next=<path>` instead of returning a raw 401. The
`?next=` is the URL-encoded original path + query string (e.g.
`?next=%2Fsearch%3Fq%3Dhello%2520world`); the chooser passes it
through to the OAuth state so the user lands back where they
started after auth. Programmatic clients (curl, scripts) still
receive 401 + `WWW-Authenticate: Bearer` so they can retry
correctly — the redirect is gated on `Accept: text/html` so an
API consumer doesn't see a 302 when it expected a 401.

Session storage is in-memory only by default — server restart logs
everyone out. The store interface is small enough to swap for a
DB-backed implementation later if operators ask for it. Sliding
renewal (every authenticated request extends the cookie's
expiry by `auth.session_ttl`) keeps an active user signed in
indefinitely; an idle user is logged out after one TTL.

The session cookie name defaults to `ragabast_session`; override
with `auth.cookie_name` (env `AUTH_COOKIE_NAME`) when running
multiple ragabast instances behind the same host.

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

### Ingest preflight (skip re-sending unchanged documents)

Ingest pipelines that re-scan a docbuilder tree every few
minutes can avoid shipping the document body when ragabast
already has an up-to-date copy. `GET /api/documents/{uid}/fingerprint`
returns the content fingerprint ragabast last stored for that
UID; the client compares against the document's
frontmatter fingerprint and decides whether to send the
full document:

```
GET /api/documents/adr-001/fingerprint
```

Response when the doc has been ingested:

```json
{
  "uid": "adr-001",
  "fingerprint": "sha256:abc123...",
  "ingested_at": "2026-09-29T10:00:00Z"
}
```

Response when the UID has never been ingested: `404 Not Found`.

**The fingerprint is authoritative in the frontmatter.**
docbuilder writes `fingerprint: <hex>` as the last field of
every doc's YAML frontmatter (see
[`github.com/inful/mdfp`](https://github.com/inful/mdfp) —
`CalculateFingerprint` produces `sha256(body)`, where body
is the markdown content WITHOUT the YAML frontmatter
delimiters or the fingerprint field itself). ragabast's
ingest path reads that exact value and stores it. The
client should read the same field from its local copy and
compare — never recompute. This avoids the client having
to mirror the docbuilder algorithm version, parse the
frontmatter to strip the fingerprint line before hashing,
or stay in sync with future changes to the hash function.

Client flow:

1. Read the `fingerprint:` value from the document's YAML
   frontmatter. Same string ragabast stored at ingest time
   (assuming the file was generated by docbuilder, which is
   the canonical case for files in a docbuilder tree).
2. `GET /api/documents/{uid}/fingerprint`
3. **404** → never been ingested; proceed with `POST /api/ingest`.
4. **200 with matching fingerprint** → already up to date; skip
   the `POST /api/ingest` entirely (this is the fast path
   for the steady-state "nothing changed" case).
5. **200 with different fingerprint** → source has changed;
   re-ingest via `POST /api/ingest`.
6. **No `fingerprint:` in the local frontmatter** → docbuilder
   hasn't been run on this file (or the field was manually
   stripped). Skip preflight, upload unconditionally. The
   next ingest path will accept the doc and the parser
   will auto-generate a fingerprint if the field is missing.

A worked bash example showing the full client loop with
the multipart `/api/ingest/file` endpoint is in
[`examples/ingest-with-preflight.sh`](examples/ingest-with-preflight.sh).
It's a single ~120-line script that takes a UID and a
markdown file path, reads the frontmatter fingerprint with
`awk`, calls the preflight endpoint, and only uploads when
the fingerprints disagree. Requires `bash`, `curl`, `awk`,
`jq` — all standard on Linux + macOS.

```bash
export RAGABAST_URL=https://ragabast.example.com
export AUTH_TOKEN=...
./examples/ingest-with-preflight.sh adr-001 path/to/adr-001.md
# skip  adr-001  fingerprint unchanged (3a7f...)
# ... or
# ingest adr-001  new document (local fp=3a7f...)
# {"message":"Document ingested successfully","document_id":"adr-001","chunks":2}
```

The endpoint is cheap (one chromem-go query against the
`document_id` metadata filter using a dummy embedding), so
calling it on every doc in a batch ingest is fine. Auth,
rate-limit, and per-document size cap semantics are the same
as `/api/ingest` — a hostile client can't bypass the limits
by spamming preflight.

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

Google's native Gemini embedding endpoint is **not** OpenAI-compat.
ragabast speaks it via a separate `embedding_gemma` provider
(see the [Embedding provider](#embedding-provider) section
below) — operators pointing at the native Gemini endpoint or any
`/v2/embed`-style local proxy can switch providers with a single
config flag.

The same caveat applies to Google's other embedding models
(`gemini-embedding-001`, `gemini-embedding-2`),
which work today only because Google's OpenAI-compat shim
translates the wire format; the `dimensions` field is silently
ignored by that shim, so Matryoshka truncation has no effect
against Google-hosted embeddings.

## Embedding provider

ragabast picks the embeddings client from `embedding_provider`
(env `EMBEDDING_PROVIDER`). Two values are supported:

| Value | Wire | Server |
|---|---|---|
| *(empty)* / `openai` | `POST /v1/embeddings`, `{"input": [...], "model": "..."}` | Ollama 0.5+, vLLM, LM Studio, llama.cpp `--embedding`, llama-stack, OpenAI, Google's OpenAI-compat shim |
| `embedding_gemma` | `POST /v2/embed`, `{"texts": [...]}` | EmbeddingGemma `/v2/embed` endpoint, native Gemini embedding proxy, any custom server speaking this shape |

The `embedding_gemma` provider speaks the wire shape used by
Google's hosted EmbeddingGemma endpoint and by local proxies
that wrap it:

```bash
curl -sS --fail-with-body 'https://embedder.local.net/v2/embed' \
  -H 'Content-Type: application/json' \
  --data '{"texts":["text for embedding","second text for embedding"]}'
```

Key differences from the OpenAI-compat path:

- **No `model` field.** The server picks the model itself. ragabast doesn't send one and the response doesn't have to identify which model produced each vector.
- **No `dimensions` request field.** Configure `vectordb.embedding_dimension` to match what the server returns; the client only checks for mismatches and warns.
- **No `Authorization` header.** The `/v2/embed` endpoint is unauthenticated by convention. If your proxy requires auth, front it with a header-injecting middleware or extend the client.
- **Response shape is probed.** The client accepts `{"embeddings":{"float":[[...]]}}` (canonical EmbeddingGemma / Cohere-style — this is what most production EmbeddingGemma servers return), `{"embeddings":[[...]]}` (bare parallel-of-texts), `{"results":[{"embedding":[...]}]}`, and `{"data":[{"embedding":[...]}]}`. Unknown shapes surface the raw body in the error so you can pin down what your server actually returns and either patch the client or your proxy.

To switch an existing install to the EmbeddingGemma path:

```yaml
embedding_provider: embedding_gemma
embedding_gemma:
  base_url: https://embedder.local.net
  embedding_dimensions: 768        # must match what the server returns; mismatch logs a one-shot WARNING
  embedding_concurrency: 4         # parallel worker pool — same semantics as ollama.embedding_concurrency
  embedding_doc_prompt: "title: none | text: "      # recommended for embedding-gemma
  embedding_query_prompt: "task: search result | query: "
  timeout: 30s
```

Or via env vars:

```bash
EMBEDDING_PROVIDER=embedding_gemma
EMBEDDING_GEMMA_BASE_URL=https://embedder.local.net
EMBEDDING_GEMMA_DIMENSIONS=768
EMBEDDING_GEMMA_DOC_PROMPT="title: none | text: "
EMBEDDING_GEMMA_QUERY_PROMPT="task: search result | query: "
```

`ragabast doctor` probes the embeddings server with the
configured provider, so dimension mismatches show up there
before the first ingest crashes with "vectors must have the
same length".

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
