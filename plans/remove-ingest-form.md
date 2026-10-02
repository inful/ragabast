# PR 2: Remove the HTML `/ingest` form

> Status: **planned**. Prerequisite: [PR 1 — frontmatter title
> extraction](https://github.com/inful/ragabast/pull/95) merged.

## Objective

Drop the HTML `/ingest` form from the web UI and stop rendering
the `Ingest` link in the navigation. Keep the Huma HTTP API
(`POST /api/ingest`, `POST /api/ingest/raw`, `POST /api/ingest/file`)
— the form was the canonical "paste arbitrary markdown" UX
surface, and the API endpoints are out of scope per the
operator's direction.

## Scope

1. **Remove the `/ingest` HTML route.** Delete the chi route
   handler that renders `ingest.html` and the corresponding
   POST handler that calls `Service.IngestDocument`. The Huma
   `/api/ingest` family stays.
2. **Drop the `ingest.html` template from the FS template
   embed.** If no other page references it, the file goes
   entirely. If a fallback path in `internal/web/fallback_renderers.go`
   still references it, the fallback entry goes too (and the
   `TestFallbackRenderers_*` test that pins the contract gets
   updated accordingly).
3. **Drop the `Ingest` link from the nav.** The header
   partial (or wherever the nav is composed) gets one fewer
   entry. No deep-link regression: the `/ingest` URL will
   404, which is the intended behaviour post-removal.
4. **Stale-comment cleanup (carry-over from PR 1 review).**
   Two comments still describe the OLD `doc.Title` extraction
   path (H1 from markdown) and were not updated in PR 1.
   Both files are in scope for PR 2 anyway, so they get
   fixed as part of the same PR:
   - `internal/web/fallback_renderers.go:21` — change
     "doc.Title from the first H1 header" to
     "doc.Title from the frontmatter `title:` field".
   - `internal/web/handlers.go:116` — change
     "doc.Title from the H1 header" to
     "doc.Title from the frontmatter `title:` field".

## Out of scope (deferred)

- The Huma HTTP ingest endpoints (`/api/ingest`,
  `/api/ingest/raw`, `/api/ingest/file`). Operators who want
  to ingest from outside the web UI keep those. The
  `web_upload` literal in the JSON-arity paths is a
  remaining UX wart but is not addressed by this PR.
- Curl-friendly ingestion via the API. If the operator
  wants a CLI wrapper around the HTTP API, that's a
  separate PR.
- The `DocumentFilePath = "web_upload"` placeholder in the
  vector layer. With PR 1's frontmatter contract, the only
  documents that produce `DocumentFilePath = "web_upload"`
  going forward are ones submitted via the JSON-arity HTTP
  endpoints that don't supply a `filename`. The display
  layer's fallback chain (title → filename → UID) means the
  literal only surfaces when the doc also has no
  frontmatter `title:` AND no body H1 — a rare case after
  PR 1.

## Tests (TDD-shaped)

- `TestIngest_RouteRemoved` — `GET /ingest` returns 404
  (not 405 / 200 / 500).
- `TestIngest_APISurfaceUnchanged` — POST
  `/api/ingest`, `/api/ingest/raw`, `/api/ingest/file`
  still ingest a valid docbuilder 200 OK. Pins the
  "API endpoints stay" contract.
- `TestNav_NoIngestLink` — render the nav and assert
  the `Ingest` entry is not present.
- Updated fallback-renderer tests if the `ingest.html`
  fallback entry is removed (see Scope item 2).

## Acceptance criteria

- `GET /ingest` → 404.
- `POST /ingest` → 404 (no longer accepting form-encoded
  bodies).
- Huma `/api/ingest*` endpoints still work end-to-end
  via humatest.
- Nav has no `Ingest` link.
- The two stale comments in the web package are updated.
- `go test -race -count=1 ./...` clean.
- `golangci-lint run ./...` clean.
- No drive-by changes.

## Risks

- **Deep links break.** Operators who bookmarked `/ingest`
  will get a 404. Mitigation: the operator's stated
  intent was to remove the form, so this is the desired
  behaviour. No redirect to a "this moved here" page —
  keep the surface area small.
- **API drift regression.** If the Huma endpoints are
  accidentally affected by the route removal, ingest
  breaks. Mitigation: the `TestIngest_APISurfaceUnchanged`
  test pins this contract.