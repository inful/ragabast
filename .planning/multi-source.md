# Multi-source ingestion: Stage 1 (backend) + Stage 2 (UX)

## Goal

Make ragabast aware that documents come from different sources, and
let the operator *try out* three multi-source behaviors from chat and
search:

1. **Subset scoping** — restrict a chat's retrieval to one or more
   source kinds (e.g. "only GitLab issues"). Per-session, controlled
   by the chat form.
2. **Cross-source reasoning** — let the LLM see which source kind
   each retrieved chunk came from, so it can attribute claims
   ("this is from a closed GitLab issue, this is from a doc").
3. **Clustered presentation** — group retrieved chunks by source
   kind in the chat sources panel, so an operator skimming a
   multi-source reply can see "3 from GitLab, 2 from docs".

The work is split into two concrete stages:

- **Stage 1 (backend):** the data shape, the filter, and the per-
  source citation dispatch. Backend-only; a curl operator can
  exercise all three behaviors end-to-end.
- **Stage 2 (UX):** the chat form gains a source-kind multi-select;
  the chat sources panel groups chunks by source kind; per-source
  icons make citations scannable. The multi-select is the previous
  selection's default on each new question — sticky, but the
  operator can change it per-question.

The experiment is bounded to **docbuilder + GitLab issues**. New
sources (a second issue tracker, source code, etc.) are not in
scope for these two stages and are not designed for here. If the
experiment validates the use case, the Stage 1 backend shape is
designed to absorb additional sources without rewriting the
pipeline, but that is a future decision.

## Why now

A specific bug drives the timing: today the citation layer in
`internal/service/inline_links.go` has a single global citation
preference order (`DocbuilderURL → DocumentURLs[0] → empty`). When
`ragabast.docbuilder_base_url` is configured, every citation points
at the synthetic docbuilder permalink — including for documents that
came from GitLab, where the operator expects the citation to land
on the original GitLab issue. This was uncovered during review of
the handlers-split refactor.

Stage 1 fixes the bug as part of a larger, well-motivated change.
Stage 2 is sequenced immediately after because the multi-source
experiment isn't useful from a curl prompt — the operator needs to
see the source-kind grouping and filter interactively to evaluate
whether the feature is worth investing in further.

## Stage 1 scope (backend)

### 1. Source identity on every chunk

Add a typed `SourceKind` to `models.Document`, `models.Chunk`, and
`models.SearchResult`:

```
type SourceKind string
const (
    SourceUnknown    SourceKind = ""
    SourceDocbuilder SourceKind = "docbuilder"
    SourceGitLab     SourceKind = "gitlab"
)
```

Population rule: each writer sets it at write time. The docbuilder
ingest writer (`internal/web/huma_ingest.go`, the async batch path)
sets `SourceDocbuilder`. The gitlab writer
(`internal/web/huma_ingest_gitlab.go`) sets `SourceGitLab`. Existing
chunks in the vector store backfill best-effort on read (UID starts
with `gitlab:` ⇒ `SourceGitLab`, otherwise `SourceDocbuilder`).

Backwards compatibility: zero-value `SourceUnknown` is treated as
`SourceDocbuilder` everywhere it matters. A migration window where
old corpora keep working without backfill.

### 2. Per-source citation URL

Replace the hardcoded order in `sourceLinkURL` with a small per-kind
dispatch:

| SourceKind | Citation URL preference |
|---|---|
| `SourceGitLab` | `DocumentURLs[0]` (the original GitLab `web_url`) |
| `SourceDocbuilder` | `DocbuilderURL` → `DocumentURLs[0]` (existing behavior) |
| `SourceUnknown` | `DocbuilderURL` → `DocumentURLs[0]` (existing behavior, treats as docbuilder) |

The gitlab writer must ensure `DocumentURLs[0] = web_url` on every
ingest (it already does; verify the field ordering in
`internal/gitlab/payload.go`).

### 3. Chunk metadata bag

Add `Metadata map[string]string` to `models.Chunk` (persisted via
`bson`/`json`, similar to `DocumentTags`). The gitlab writer
populates:

| Key | Value |
|---|---|
| `state` | `"open"` / `"closed"` (from `IssuePayload.State`) |
| `author_username` | author's GitLab handle (from `Author.Username`) |

The vector store persists it; the search filter layer reads it.
Field conventions are documented per-source, not enforced globally —
the gitlab package owns its metadata keys.

### 4. Search filter for source kinds

Extend `vector.SearchFilters` with `SourceKinds []SourceKind`.
Empty = "all sources" (current behavior). Non-empty = "restrict to
these kinds". The vector layer applies it as a post-filter on chunk
metadata (cheap; same shape as the existing date filters).

The chat handler (`internal/web/chat_handlers.go`) reads the form
field `source_kinds` (comma-separated source kinds), parses, and
threads through `service.SearchFilters`. When the UI lands in Stage
2, it populates this same field.

### 5. Cross-source reasoning: light LLM context

The chat prompt context is built in
`internal/service/query_context.go` — specifically
`buildQueryContextItems` and `formatContextEntry`, returning a
`[]string` that is joined into the system prompt. Stage 1 adds a
prefix line `[source:<kind>]` to each entry's format string when
`SourceKind` is non-empty. No re-ranking; no ingest-time cross-
references. This is what the user asked for ("light") and is enough
for the LLM to attribute claims correctly when answering
"does A from gitlab impact B from docs?"

The chat sources panel template renders whatever shape the handler
passes; no template changes in Stage 1 — the data is just available
for Stage 2.

### 6. Clustered presentation data shape (backend prep only)

The chat handler returns chunks sorted by source-kind before passing
to the template. The template can render them grouped by iterating
in order; today it doesn't, but the order is stable. Stage 2
implements the grouping; Stage 1 only ensures the data is in the
right order.

## Stage 1 file changes (concrete)

| File | Change |
|---|---|
| `internal/models/document.go` | Add `SourceKind` field, default `SourceUnknown` |
| `internal/models/chunk.go` | Add `SourceKind`, `Metadata` fields |
| `internal/vector/search_index.go` | Add `SourceKinds []SourceKind` to `SearchFilters`; honor it in `Search` |
| `internal/service/inline_links.go` | Replace hardcoded citation order with per-kind dispatch |
| `internal/service/query.go` | Pass `SourceKinds` through; prefix LLM context with `[source:<kind>]` |
| `internal/service/ingest.go` (or wherever chunks are produced) | Populate `SourceKind` from the ingest path; preserve `Metadata` |
| `internal/web/huma_ingest.go` (docbuilder path) | Set `SourceKind = SourceDocbuilder` |
| `internal/web/huma_ingest_gitlab.go` | Set `SourceKind = SourceGitLab`; thread `state` / `author_username` into `Metadata` |
| `internal/web/chat_handlers.go` | Read `source_kinds` form field; thread into `SearchFilters` |
| `internal/gitlab/payload.go` | Verify `web_url` lands in `DocumentURLs[0]` (already does) |
| Tests | Direct tests for the citation dispatch per source kind; filter tests; chat prompt test for source-prefixed context |

## Stage 1 commit sequence (conventional)

1. `feat(models): add SourceKind enum and chunk Metadata bag`
2. `feat(vector): honor SourceKinds in SearchFilters`
3. `feat(service): per-source citation URL dispatch`
4. `feat(web): per-session source-kind filter on /chat/message`
5. `feat(web/gitlab): populate SourceKind and Metadata on ingest`
6. `feat(web/docbuilder): populate SourceKind on ingest`
7. `feat(service): prefix LLM context with source kind`
8. `test(...): direct tests for citation dispatch and source filter`

Stage 1 fits one merge PR (squash) or 2-3 reviewable PRs if the
reviewers prefer smaller drops.

## Stage 2: UX follow-ups

Once Stage 1 lands, the backend is end-to-end usable from curl.
Stage 2 makes the multi-source experiment usable from the actual
chat UI so the operator can evaluate whether the feature is worth
keeping.

### 2.1 Source-kind multi-select on the chat form

The chat form (`templates/chat.html` and the corresponding
`handleChatPage` rendering in `internal/web/chat_handlers.go`)
currently submits the user query and the chat session id. Stage 2
adds a multi-select (chip-style checkboxes or a Bulma select
multiple) for source kinds. UI rule: empty = "all sources"
(matches Stage 1 backend semantics). The form submits a
comma-separated `source_kinds` value that the handler already
parses.

### 2.2 Chat sources panel: group by source kind

The chat response (`templates/chat_message.html` or the
`sources` block within it) currently renders retrieved chunks as a
flat list. Stage 2 groups them by source kind:

- Section header per kind ("From GitLab: 3 chunks", "From Docs:
  2 chunks"). Within a kind, chunks sort by similarity (today's
  default).

Stage 1 already returns chunks sorted by source kind. The Stage 2
work is template + handler-side data shaping only — no service-
layer changes.

Time-window grouping (sub-bucket within a kind by month/year) is
explicitly **not** in scope for Stage 2 — see "What Stage 2
explicitly does NOT do".

### 2.3 Per-source visual markers in citations

Each `[src:N]` marker the LLM emits, and the corresponding citation
link rendered in the sources panel, gets a small icon indicating
its source kind (e.g. a GitLab mark for `gitlab-issue`, a document
glyph for `docbuilder`). Small change: extend `models.SearchResult`
or a helper to carry a `SourceIcon()` per kind, render in the
template. Affects `internal/web/chat_handlers.go` and the chat
response template.

### 2.4 Sticky default with per-question override

The source-kind multi-select is a **per-question** toggle: each
chat message submits its own `source_kinds` value (or empty for
"all sources"). However, the form **pre-fills with the operator's
previous selection** within a chat session (the cookie-pinned
session id from issue #22). So:

- Operator selects "GitLab + Docs" on message 1 → it's the default
  on message 2.
- Operator changes to "Docs only" on message 3 → that becomes the
  default on message 4.
- Operator clears the selection on message 5 ("all sources") →
  empty becomes the default on message 6.

Implementation: the chat session store gains a
`SourceKinds []string` field recording the last non-empty
selection; the chat handler reads it on GET to pre-fill the form,
and writes back the current submission (if non-empty) on POST.
Clearing the selection on the form (sending empty) does *not*
clear the stored default — the next form still pre-fills with the
last non-empty choice. This avoids the surprising "I unchecked
the box and now I can't get back to my previous scope" UX.

Saved subsets named by the operator ("engineering-only",
"customer-facing-only") are deferred — not needed for the
experiment.

## Stage 2 file changes (concrete)

| File | Change |
|---|---|
| `templates/chat.html` | Add source-kind multi-select; pre-fill with previous selection |
| `templates/chat_message.html` (or sources block) | Group chunks by source kind; per-kind icons |
| `internal/web/chat_handlers.go` | Read multi-select on GET (pre-fill from session); render grouped sources on POST; write submission back to session |
| `internal/web/chat_sessions.go` (or equivalent) | Extend session store with `SourceKinds []string` (last non-empty selection) |
| `internal/models/source_kind.go` (new) | `SourceIcon()` helper returning the per-kind marker glyph/HTML |
| Tests | Template rendering tests for grouped output; session-store round-trip for SourceKinds (write, read, "do not clear on empty submission"); form-parsing tests |

## Stage 2 commit sequence (conventional)

1. `feat(models): SourceIcon() helper per source kind`
2. `feat(web/sessions): persist SourceKinds as sticky default across chat messages`
3. `feat(web): group chat sources by source kind`
4. `feat(web): per-source icons in citations`
5. `feat(web): source-kind multi-select on chat form (pre-fills with previous selection)`
6. `test(...): template + session-store round-trip tests`

Stage 2 fits one PR or 2 reviewable PRs.

## Decisions captured

- **SourceKind as typed string enum.** An `int` would lose the
  `kind` ↔ wire-shape mapping; a struct would be over-engineering
  for what is currently a closed set.
- **Metadata as `map[string]string`, not typed fields.** Future
  sources will surprise us with new metadata fields. Free-form is
  honest about what we know today.
- **Light cross-source reasoning.** No ingest-time cross-reference
  extraction. The prompt prefix is enough for the use case.
- **Per-session source scoping.** Per-user or per-conversation
  scoping are deferred.
- **"Cluster" = group by source kind in the results panel.** Time-
  window sub-bucketing and topic clustering are explicitly not in
  scope (see "What Stage 2 explicitly does NOT do").
- **Scope is bounded to docbuilder + GitLab.** New sources are a
  future decision; Stage 1 is designed to absorb them, but is not
  built around their specifics.

## Open questions

- **Backfill of `SourceKind` on read.** Best-effort UID-prefix
  inference, or refuse to read chunks with empty `SourceKind` until
  re-ingested? Default is best-effort with a one-time backfill
  utility (a small one-shot script; destructive operations are
  CLI-only per AGENTS.md).
- **`Metadata` key naming convention.** `snake_case` (`state`,
  `author_username`). Centralize the key constants in
  `internal/gitlab/` so typos surface as compile errors within the
  package.
- **Should the chat prompt prefix be `[source:gitlab] Title` or
  `[gitlab:group/bar#42] Title`?** The latter carries more
  provenance (namespace + identifier) and helps the LLM distinguish
  issues within the same source kind. Decide during implementation;
  default to `[source:<kind>] Title` for now.

## What Stage 1 explicitly does NOT do

- Add UI (that's Stage 2).
- Build a polymorphic `Source` interface.
- Add per-source chunkers.
- Pre-compute cross-references at ingest time.
- Change the gitlab ingester's wire envelope (no migration burden
  on existing senders).

## What Stage 2 explicitly does NOT do

- Time-window grouping within a source (sub-bucket by month/year).
- Saved subsets named by the operator.
- Per-source styling beyond the icon glyph.
- Cross-source links / "see also" indexing.
- Topic clustering or semantic grouping of chunks within a source.
- New ingest sources.