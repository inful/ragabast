# Security

ragabast is a single-tenant local-first RAG tool. The defaults are safe
for local development; the operator is responsible for the additional
hardening needed when exposing the web service on a non-loopback
interface. This document is the operator's guide.

## Threat model

In scope:

1. **Network-reachable attacker** who can issue HTTP requests to the
   listener but does not have shell access to the host.
2. **Attacker who has ingested a malicious document** — a doc with
   `<script>` in the title, `javascript:` URLs in the `urls:`
   frontmatter, or prompt-injection payloads in the body — and wants the
   operator's browser to execute it via the chat / search UI.
3. **Insider on the host** (read access to `config.yml` or the env
   vars) — secrets in plaintext are an accepted risk in exchange for
   operational simplicity.

Out of scope:

1. Kernel or container escape; sandbox escapes in the LLM provider.
2. Multi-user authentication — there is exactly one credential, the
   shared bearer token.
3. Tampering with the on-disk vector store by an attacker with write
   access to `data/vectors/` — `vector reset --force` is the
   recovery path; treat the persistence directory as a trusted
   boundary.

## Hardening checklist (operator)

1. **Set `server.auth_token`** to a high-entropy random string before
   exposing the listener on anything other than `127.0.0.1`:
   ```bash
   openssl rand -hex 32
   # paste into config.yml under server.auth_token, or:
   SERVER_AUTH_TOKEN=$(openssl rand -hex 32) ragabast serve
   ```
   Without this, every endpoint listed under "Public routes" below is
   open. With it, every other endpoint requires
   `Authorization: Bearer <token>`.

2. **Keep `config.yml` at `0600`.** `ragabast config init` writes
   `0o600`; respect this when you copy the file. If you check it
   into version control, encrypt it (sops, age, git-crypt).

3. **Set `server.cors_origins`** explicitly when enabling CORS. The
   wildcard `*` is supported for trusted local-only deployments; the
   default empty list disables cross-origin browser requests
   entirely.

4. **Set `server.rate_limit_per_minute`** to a value your legitimate
   browser usage will not trip (the chat form is the only
   consumer; a few clicks per second is a comfortable default).
   `0` disables the limiter and is only safe when the listener is
   on `127.0.0.1`.

5. **Put ragabast behind TLS.** The web server speaks cleartext
   (`http.Server.ListenAndServe`). For non-loopback deployment,
   terminate TLS in a reverse proxy (nginx, Caddy, Traefik) or in
   front of the Docker container. The `Strict-Transport-Security`
   header the server emits is a no-op without TLS but does no harm.

6. **Trust the proxy that sets `X-Forwarded-For`.** chi v5.3.0+
   validates the proxy chain for IP spoofing. If you terminate TLS
   in nginx and forward to ragabast, configure `middleware.RealIP`
   trusted proxies accordingly.

## Endpoint surface

### Public routes (always open)

These remain reachable without `Authorization` so the operator's
browser can render the form chrome and load static assets. State-changing
endpoints (POST, DELETE) are NOT in this list — every one of them
requires the bearer token.

| Method | Path |
|---|---|
| GET | `/`, `/chat`, `/search`, `/ingest`, `/documents` |
| GET | `/static/*` |
| OPTIONS | `*` (CORS preflight) |

### Protected routes

Every other route requires `Authorization: Bearer <token>` when
`server.auth_token` is configured:

- `GET /api/health`, `/api/tags`, `/api/categories`, `/api/tags-categories`
- `POST /api/query`, `/api/search`, `/api/link-suggestions`, `/api/frontmatter/suggest`
- `POST /api/ingest`, `/api/ingest/raw`, `/api/ingest/file`
- `GET /api/documents`, `DELETE /api/documents/{id}`, `POST /api/documents/prune`
- `POST /chat/message`, `POST /search`, `POST /ingest`

### Rate-limited routes

The LLM-backed endpoints share a per-client-IP token bucket. Health
checks, catalog reads, ingest, and HTML page renders are NOT throttled:

- `POST /api/query`, `/api/search`, `/api/link-suggestions`, `/api/frontmatter/suggest`
- `POST /chat/message`, `POST /search`

`429 Too Many Requests` is returned with a `Retry-After` header when the
bucket is empty.

## HTTP hardening shipped by default

Every response, regardless of route, carries:

- `X-Content-Type-Options: nosniff`
- `Referrer-Policy: no-referrer`
- `X-Frame-Options: DENY`
- `Content-Security-Policy: default-src 'self'; script-src 'self' https://unpkg.com; style-src 'self' https://cdn.jsdelivr.net; img-src 'self' data:; frame-ancestors 'none'; base-uri 'self'; form-action 'self'`
- `Strict-Transport-Security: max-age=63072000; includeSubDomains`

Every request body is capped at 10 MiB (Huma endpoints use Huma's own
cap; form endpoints use `http.MaxBytesReader` in
`internal/web/max_bytes.go`). The `http.Server` literal applies the
configured `ReadTimeout` and `WriteTimeout`, plus a fixed 120 s
`IdleTimeout` — slowloris and slow-body attacks cannot pin
connections open.

## LLM reply sanitization

`renderChatMarkdownToSafeHTML` runs the LLM reply through `goldmark` +
`bluemonday.UGCPolicy`. The result is then re-sanitized by
`sanitizeForChatHTML` (`internal/web/markdown.go`) immediately before
the `template.HTML` wrap. The double pass is defense in depth: a
single bug in the markdown pipeline, `InlineSourceLinks`, or
bluemonday itself does not become XSS through the chat UI.

## Secrets and logging

- `ragabast config init` writes `config.yml` with mode `0o600`
  (owner read/write only). SaveConfig also chmods an existing
  file to `0o600` before overwriting it, so the
  `config init --force` path stays safe.
- The access logger redacts values for `query`, `message`, `text`,
  `content`, `document_id`, and `docbuilder_base_url` query keys
  before chi's logger formats the line. Non-sensitive keys
  (e.g. `tag`, `category`) pass through unchanged.
- The bearer token is **never** logged. When
  `ragabast.log_chat_requests` is true, only the chat request /
  response bodies are logged under the `[chat-debug]` prefix;
  the `Authorization` header is never written.

## Reporting vulnerabilities

Open a private security advisory on GitHub
(`github.com/inful/ragabast/security/advisories/new`) with:

- A description of the issue and its impact.
- Reproduction steps (config, request, expected vs. actual).
- Your ragabast version (`ragabast --version`) and Go version
  (`go version`).

We aim to acknowledge within two business days and to publish a
fix within 30 days for high-severity findings.

## Versioning policy

Security fixes land on `main` and ship on the next patch release
(SemVer: `0.1.x → 0.1.x+1`). Breaking security changes ship on the
next minor release with a `SECURITY.md` migration note.
