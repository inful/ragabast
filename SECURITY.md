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
2. Per-user authorization (RBAC, group-to-role mapping, per-user
   audit attribution beyond what `/auth/me` and the access log's
   `auth_label` already capture). The OAuth/OIDC login identifies
   the user; every authenticated user currently has the same
   privileges as the bearer-token holder.
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
browser can render the form chrome, load static assets, and complete
the OAuth handshake. State-changing endpoints (POST, PUT, PATCH,
DELETE) are NOT in this list — every one of them requires either the
bearer token or a valid session cookie (CSRF-protected).

| Method | Path |
|---|---|
| GET | `/`, `/chat`, `/search`, `/ingest`, `/documents` |
| GET | `/auth/login`, `/auth/<provider>/login`, `/auth/<provider>/callback`, `/auth/me` |
| GET | `/static/*` |
| OPTIONS | `*` (CORS preflight) |

When OAuth is configured, the browser UX is:

- `GET /auth/login` — chooser page (auto-redirects to the single
  configured provider when only one exists).
- `GET /auth/<provider>/login` — starts the OAuth 2.0 Authorization
  Code + PKCE flow; redirects to the IdP.
- `GET /auth/<provider>/callback` — verifies the state cookie,
  exchanges the code, fetches the userinfo / ID-token claims, mints
  the session cookie, redirects to `?next=<original URL>`.
- `POST /auth/logout` — destroys the session, clears the cookie,
  redirects to `/auth/login`.
- `GET /auth/me` — JSON: `{authenticated: bool, subject, username,
  email, name, provider, role}` for the UI's "signed in as" widget.

Browser protection: when OAuth is configured and a browser hits a
protected route without a session, the middleware redirects to
`/auth/login?next=<path>` rather than returning a raw 401 — a 401
would force the user to copy a URL into the address bar. Programmatic
clients (curl, scripts) still receive 401 + `WWW-Authenticate:
Bearer` so they can retry correctly; the redirect is gated on
`Accept: text/html`.

### Protected routes

Every other route requires `Authorization: Bearer <token>` when
`server.auth_token` is configured:

- `GET /api/health`, `/api/tags`, `/api/categories`, `/api/tags-categories`
- `POST /api/query`, `/api/search`, `/api/link-suggestions`, `/api/frontmatter/suggest`
- `POST /api/ingest`, `/api/ingest/raw`, `/api/ingest/file`
- `GET /api/documents`, `DELETE /api/documents/{id}`, `POST /api/documents/prune`
- `POST /chat/message`, `POST /search`, `POST /ingest`

### Rate-limited routes

The LLM-backed and ingest endpoints share a per-client-IP token
bucket. Health checks, catalog reads, and HTML page renders are NOT
throttled:

- `POST /api/query`, `/api/search`, `/api/link-suggestions`, `/api/frontmatter/suggest`
- `POST /chat/message`, `POST /search`
- `POST /api/ingest`, `/api/ingest/raw`, `/api/ingest/file`, `POST /ingest`

`429 Too Many Requests` is returned with a `Retry-After` header when the
bucket is empty.

### Async ingest queue (audit log)

`POST /api/ingest/async` accepts a document and returns `202
Accepted` immediately; a bounded worker pool drains the queue in
the background. Every state transition is logged:

```
ingest job event=ingest.job.started job_id=… remote_addr=… bytes=…
ingest job event=ingest.job.completed job_id=… document_id=… chunks=…
ingest job event=ingest.job.failed job_id=… error=…
```

Jobs persist to `<async_ingest_queue_dir>/<job_id>.json` (mode
`0700`); a restart during a long import resumes from where the
process died. The in-memory channel is a transient cache; the
on-disk file is the source of truth.

### Session storage (OAuth login)

When `auth.providers` is non-empty, the server mints an in-memory
`Session` record after a successful OAuth callback. Sessions are
keyed by the 64-char hex value of `auth.cookie_name` (default
`ragabast_session`), stored in an HTTP-only, `SameSite=Lax`,
Secure-when-TLS cookie.

- **Storage**: in-memory only. Server restart logs everyone out.
  The store interface is small enough to swap for a DB-backed
  implementation later; until then, do not configure OAuth for a
  multi-replica deployment without sticky sessions or shared
  session storage.
- **TTL**: `auth.session_ttl` (default 12h, env
  `AUTH_SESSION_TTL`). Sliding renewal — every authenticated
  request through the middleware extends the session's expiry
  by the configured TTL.
- **Expiry enforcement**: lazy on `Get` (evicts the expired entry
  on read) plus a `Count()` accessor that operators can poll.
  No background sweeper today; add one when traffic warrants.
- **Cookie name**: `auth.cookie_name` (env `AUTH_COOKIE_NAME`).
  Operators running multiple ragabast instances behind different
  paths or ports should set distinct names so cookies don't
  collide.
- **CSRF on sign-out**: `POST /auth/logout` is a state-changing
  request. The CSRF middleware requires the form's `csrf_token`
  hidden field to match the `ragabast_csrf` cookie. A cross-origin
  attacker cannot make the browser send the cookie on a POST, so
  the sign-out flow is safe by the same mechanism as `/chat/message`.
- **`AllowedUsers` allowlist**: when set on a provider, only the
  listed usernames/emails can complete login. Empty = accept any
  user the IdP authenticated. Match is exact-string equality (no
  glob, no regex) so a typo means "nobody".

## HTTP hardening shipped by default

Every response, regardless of route, carries:

- `X-Content-Type-Options: nosniff`
- `Referrer-Policy: no-referrer`
- `X-Frame-Options: DENY`
- `Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'self'; form-action 'self'`

> Note: the CSP is strictly self-hosted. Bulma (CSS) and htmx (JS) ship inside the binary at `internal/web/static/` and are served from `/static/*`; no third-party CDN is reachable for page chrome. The historical build whitelisted `https://unpkg.com` and `https://cdn.jsdelivr.net` for those libraries — bundling them removed the requirement.
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
