# Issue 97 — Don't ingest documents that hugo will not publish

## Background

Hugo has four frontmatter markers that exclude a document from a
default build:

| Marker | Aliases | Means unpublished when |
|---|---|---|
| `draft: true` | (single key) | the field is `true` |
| `date: <future>` | (single key) | the timestamp is in the future |
| `publishDate: <future>` | `pubdate`, `published` | the timestamp is in the future |
| `expiryDate: <past>` | `unpublishdate` | the timestamp is in the past |

Source: the issue body and the Hugo docs.

Today ragabast ingests every document it sees, regardless of these
markers. After ingest the database holds the document, the chat
surface cites it, and search returns it — even though the operator's
Hugo build would not have published the source. The fix is to honor
the same publishability rules at ingest time so the two systems stay
in sync.

## Goal

When the ingest pipeline sees a document whose frontmatter says "don't
publish under Hugo's defaults":

- if the document **is already in the embeddings**, remove it.
- if the document **is not in the embeddings**, ignore the request
  silently (no error, no chunking, no embedder call).

The four markers combine with OR semantics — any one of them is enough
to filter the document.

## Non-goals

- A new HTTP endpoint for "publish-state". This PR is ingest-side
  only; nothing changes about what clients see at `/api/documents`.
- A "force re-ingest" flag to override the filter. The operator's
  intent (per the issue) is "honor Hugo"; we honor it.
- Re-ingest of previously-published documents when the date/publishDate
  rolls forward into the future. That would be a re-evaluate step,
  which is not in scope and not asked for.
- Changes to non-ingest paths. Search, chat, MCP, and the catalog do not
  consult these markers today; the PR does not introduce that
  coupling.

## Design

### Where the check lives

Two service-layer entry points exist for ingest:

- `Service.IngestDocument(ctx, content)` — `/api/documents/raw` etc.
- `Service.IngestFile(ctx, filePath)` — CLI directory ingest.

Both call `s.parser.ParseDocument(...)` followed by
`chunkAndIngest(...)`. The preflight check belongs between parse and
chunkAndIngest. A new private helper on `Service` keeps the two call
sites identical:

```go
// applyUnpublishedPreflight checks the parsed doc against Hugo's
// "don't publish" markers. If the doc is unpublished and was
// previously embedded, it is deleted. Otherwise it is ignored.
// Returns filtered=true so the caller knows to skip the chunking
// path.
func (s *Service) applyUnpublishedPreflight(
    ctx context.Context, doc *models.Document, now time.Time,
) (filtered bool, err error)
```

The check is **not** inside `chunkAndIngest`. That helper is a pure
chunk→ingest primitive shared by all callers; the preflight is an
ingest-side policy decision and should not leak into shared helpers.

### Time injection

`IsUnpublished(doc, now)` takes `time.Time` directly so the pure
detection function is deterministic. The service helper takes the
same `time.Time`. Tests call it with a fixed reference time.

### Identity in the existing API. `Service.DeleteDocument(ctx, documentID)` is the public delete; it
filters by `document_id` chunk metadata. Per the parser
(`docbuilder.go:62`), `doc.ID = doc.UID` after parse, so the lookup
value is the UID and the production chunk metadata also equals the
UID. The preflight delete uses the UID directly. No new vector-layer
method is required.

### Parser changes

Four new fields on `models.Document`:

```go
type Document struct {
    ...
    Draft       bool      // `draft: true` ⇒ unpublished
    Date        time.Time // `date: <future>` ⇒ unpublished
    PublishDate time.Time // `publishDate|pubdate|published: <future>`
    ExpiryDate  time.Time // `expiryDate|unpublishdate: <past>`
}
```

The parser's `frontmatter map[string]any` is keyed with **the case
the operator used** — yaml.v3 is case-sensitive on map keys when
unmarshaling into `map[string]any`. Hugo is case-insensitive on
field names. The PR lowercases a small set of keys (the four Hugo
fields and their aliases) into a sibling map and looks up the
lowercased key:

```go
// frontmatterLower normalizes the four Hugo field names so
// `PublishDate:`, `publishDate:`, and `publishdate:` all match.
frontmatterLower := lowerHugoKeys(frontmatter)
if v, ok := frontmatterLower["draft"]; ok { ... }
if v, ok := frontmatterLower["date"]; ok { ... }
// publishDate wins over pubdate over published.
```

Unparseable dates stay at zero time; the `IsUnpublished` check
ignores zero times, which means a typo in `date:` silently drops to
"publish" — the safe default. A non-boolean `draft:` is the same
shape: if it's not `true`, it isn't unpublished.

### Detection function

`parser.IsUnpublished(doc, now)`:

```go
func IsUnpublished(doc *models.Document, now time.Time) bool {
    if doc == nil { return false }
    if doc.Draft { return true }
    if !doc.Date.IsZero()        && doc.Date.After(now)        { return true }
    if !doc.PublishDate.IsZero() && doc.PublishDate.After(now) { return true }
    if !doc.ExpiryDate.IsZero()  && doc.ExpiryDate.Before(now) { return true }
    return false
}
```

### Service flow

```go
func (s *Service) IngestDocument(ctx, content) (*models.Document, error) {
    doc, err := s.parser.ParseDocument(...)
    if err != nil { ... }

    now := s.now()
    filtered, err := s.applyUnpublishedPreflight(ctx, doc, now)
    if err != nil { return nil, err }
    if filtered { return doc, nil }

    if err := chunkAndIngest(...); err != nil { ... }
    s.cache.Clear()
    return doc, nil
}
```

### Logging

The preflight logs through the existing service `log.Printf` channel:

- `service: removed unpublished doc <uid> from embeddings` (info)
- `service: skipping ingest of unpublished doc <uid> (not previously embedded)` (debug)

The cache invalidation already runs inside `Service.DeleteDocument`,
so a successful preflight delete also flushes the query cache.

## Files changed

| File | Change |
|---|---|
| `internal/models/document.go` | 4 new fields |
| `internal/parser/docbuilder.go` | parse `draft`/`date`/`publishDate`/`expiryDate`; case-insensitive lookup |
| `internal/parser/unpublished.go` | new file: `IsUnpublished` |
| `internal/parser/unpublished_test.go` | new file: unit tests for `IsUnpublished` |
| `internal/parser/docbuilder_test.go` | add 4–6 tests for new fields |
| `internal/service/service.go` | add `now func() time.Time` field |
| `internal/service/ingest.go` | new `applyUnpublishedPreflight`; wire into `IngestDocument` and `IngestFile` |
| `internal/service/ingest_test.go` | new tests: skip + delete-on-ingest + regression |
| `plans/issue-97.md` | this file |

## Risk mitigation

- The new preflight is gated by the frontmatter markers; a document
  without any of them is unaffected. Existing corpus: zero
  regressions.
- Cache invalidation happens through `Service.DeleteDocument`, which
  the operator already trusts. No new invalidation policy.
- The detection function takes `time.Time` directly. No
  `time.Now()` call from a goroutine that could drift; the caller
  passes the time it used for the check.
- Tests pin the **delete-then-skip** transition explicitly: ingest
  a doc with no markers → confirm it's there → re-ingest the same
  content with `draft: true` → confirm it's gone.