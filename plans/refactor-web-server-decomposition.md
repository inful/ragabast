# Refactor: web/server.go decomposition

Branch: `refactor/web-server-decomposition`

## Goal

Decompose `NewServer` (internal/web/server.go, 318 lines, cyclomatic 19, MI 17.7)
and consolidate duplicated `huma.Register` boilerplate across the seven
`huma_*.go` files. No behavior change.

## Non-goals

- No new endpoints, no new features.
- No changes to the wire shape of any API.
- No changes to the public `serviceAPI` interface.
- No dependency upgrades.

## Approach (strict TDD)

For each extracted helper:
1. Write failing tests for the helper's behavior in isolation.
2. Run the tests — confirm they fail (helper does not exist yet).
3. Extract the helper from `NewServer`. Behavior is unchanged.
4. Run the full test suite — confirm green.
5. Run `golangci-lint run --fix` — confirm clean.
6. Commit with a conventional-commit message.

## Steps

### Step 1 — `installAssetFuncs`

Extract the asset-FuncMap install + fallback-template parse from
`NewServer` (currently lines 397–411) into `*Server.installAssetFuncs()`.

**Tests added (`server_asset_funcs_test.go`):**
- `TestInstallAssetFuncs_AssetFuncRegistered` — render a template that
  uses `{{ asset "x.css" }}` and verify the URL carries the cache-busting
  query string.
- `TestInstallAssetFuncs_FallbackChatAndDocumentsParsed` — the two
  fallback pages (`chat.html`, `documents.html`) are non-nil after install.

### Step 2 — `loadTemplates`

Extract the template-loading block (currently lines 236–321, ~80 lines
of branchy reparse + FuncMap + header-block + fallback) into
`*Server.loadTemplates()`.

**Tests added (`server_templates_test.go`):**
- `TestLoadTemplates_UsesEmbeddedWhenTemplatesDirEmpty` — embedded
  templates parse successfully; `{{ asset }}` resolves; `header` block
  is registered.
- `TestLoadTemplates_FallsBackToEmbeddedWhenCustomDirFails` — set
  `TemplatesDir` to a nonexistent path; server falls back to embedded
  and still works.
- `TestLoadTemplates_OperatorSuppliedDirSuccess` — when operator
  supplies a valid dir, those templates win.

### Step 3 — `initIngestQueue`

Extract the async-ingest queue construction (currently lines 323–378)
into `*Server.initIngestQueue(svc serviceAPI)`.

**Tests added (`server_ingest_queue_test.go`):**
- `TestInitIngestQueue_NilWhenDirEmpty` — empty `AsyncIngestQueueDir`
  produces `s.ingestQueue == nil`.
- `TestInitIngestQueue_QueueCreatedWhenDirSet` — non-empty dir
  produces a non-nil `s.ingestQueue` with workers started; the
  package-level `globalIngestQueue` matches.
- `TestInitIngestQueue_ResetsGlobalState` — set the global to a
  non-nil value first, then init with empty dir, then assert the
  global is nil again (this is the test-leak fix currently in
  `NewServer`).
- `TestInitIngestQueue_DisabledOnNewQueueError` — set dir to a path
  that cannot be created; server still starts; `s.ingestQueue == nil`.

### Step 4 — `initOAuth`

Extract the OAuth / session init (currently lines 144–181) into
`*Server.initOAuth()`.

**Tests added (`server_oauth_init_test.go`):**
- `TestInitOAuth_NilWhenNoProviders` — empty `Auth.Providers` produces
  `s.oauthHandlers == nil` and `s.sessions == nil`.
- `TestInitOAuth_DefaultsCookieName` — provider configured, default
  cookie name applied.
- `TestInitOAuth_RespectsConfiguredCookieName` — provider configured,
  `Auth.CookieName` set, that name wins.
- `TestInitOAuth_DefaultsSessionTTL` — `Auth.SessionTTL == 0` results
  in a 12-hour default.
- `TestInitOAuth_NewOAuthFailsKeepsServerAlive` — provider with
  invalid config; server still starts; `s.oauthHandlers == nil`.

### Step 5 — `installMiddleware`

Extract the middleware-chain assembly (currently lines 98–223) into
`*Server.installMiddleware(router *chi.Mux, cfg *config.Config)`.

**Tests added (`server_middleware_test.go`):**
- `TestInstallMiddleware_OrderPreserved` — emit a synthetic request,
  assert the chain applied in the expected order: security headers →
  request ID → access log → recoverer → real-IP → client-IP → maxbytes
  → timeout → (CORS) → auth → CSRF → rate-limit.
- `TestInstallMiddleware_CORSSkippedWhenDisabled` — `EnableCORS=false`
  omits the CORS middleware (OPTIONS preflight returns the
  no-CORS-headers response).
- `TestInstallMiddleware_CORSAppliedWhenEnabled` — `EnableCORS=true`
  + matching origin → CORS headers on the response.
- `TestInstallMiddleware_AppliesRequestID` — the
  `requestIDFromContext` value matches a generated UUID-ish format
  after the request.
- `TestInstallMiddleware_MaxBytesCapsRequest` — POST with a body
  larger than `MaxRequestBytes` returns 413.
- `TestInstallMiddleware_TimeoutReturns504` — handler that sleeps
  longer than 60 s triggers the middleware.Timeout handler and
  produces a non-200 response.

### Step 6 — Huma boilerplate

Add `huma_op.go` with the typed `registerPOST` / `registerGET`
helpers, and `huma_ingest_op.go` with the `withIngestGuard` helper.
Migrate the three ingest operations in `huma_ingest.go` and the one
in `huma_ingest_gitlab.go` to use the ingest guard. Leave the
`huma.Register`-with-anonymous-struct form in place for the other
`huma_*.go` files (migration is mechanical and out of scope for
this refactor — the helpers exist for future use).

**Tests added (`huma_op_test.go`, `huma_ingest_op_test.go`):**
- `TestRegisterPOST_TypedHandlerInvoked` — create a Huma test API,
  register a typed handler, hit the endpoint, assert body shape and
  handler was called.
- `TestRegisterGET_TypedHandlerInvoked` — same for GET.
- `TestWithIngestGuard_AllowsAcquireAndReleases` — happy path:
  acquire returns a non-nil release; calling release doesn't panic.
- `TestWithIngestGuard_RejectsWhenLimiterFull` — saturated limiter
  returns a 429 huma error.
- `TestWithIngestGuard_RejectsWhenTooLarge` — body larger than
  maxBytes returns a 413 huma error and the release callback is nil.

### Step 7 — `NewServer` itself

After steps 1–5, `NewServer` should be a thin orchestrator. Its
existing tests (`auth_routes_test.go`, `health_full_test.go`, etc.)
cover the integration paths; no new tests are required for the
orchestrator.

**Final check:** run the full suite + lint. Both must be clean.

## Verification

After every step:
- `go test ./... -count=1` must be green.
- `golangci-lint run --fix` must produce no diagnostics.

Before the final commit:
- `go test -race ./...` must be green.
- `go test ./internal/web/... -cover` should be no lower than before
  the refactor (it should rise on most helpers because the new tests
  cover previously untested paths).

## Commit structure

1. `docs(plans): add refactor-web-server-decomposition plan` — the
   plan file itself.
2. `refactor(web): extract Server.installAssetFuncs` — step 1.
3. `refactor(web): extract Server.loadTemplates` — step 2.
4. `refactor(web): extract Server.initIngestQueue` — step 3.
5. `refactor(web): extract Server.initOAuth` — step 4.
6. `refactor(web): extract Server.installMiddleware` — step 5.
7. `refactor(web): add typed huma register helpers and ingest guard` —
   step 6.
8. `refactor(web): make NewServer a thin orchestrator` — step 7
   (mechanical call-site cleanup if needed).

If a single step becomes too large to review in one commit, split
it. Each commit's diff should be ≤ 200 lines of production code.
