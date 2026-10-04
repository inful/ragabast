# UX overhaul — comprehensive design pass

> **Status: planned.** Branch: `feat/ux-overhaul`. Off `main`. The
> daisyUI migration (`main`) and the prior visual-polish work
> (`feat/visual-polish`) are merged. This SPEC plans 30+ UX
> improvements identified during a design review of the
> post-migration UI.

## Starting point for a new instance

If you are a fresh instance picking this up, here is what to
load first, in order:

1. **Read this file end-to-end.** It is the contract.
2. **Skim the existing SPECs for context** (not for content to
   re-read):
   - `plans/daisyui-migration.md` — why daisyUI, what the
     remap table looks like, what's on main
   - `plans/visual-polish.md` — the just-merged polish work
     (login navbar fix, skip links, empty-state SVGs, contrast)
3. **Load the daisyUI skill** (the opencode `daisyui` skill
   bundle at `.agents/skills/daisyui/`). For each component
   referenced below, read the corresponding guide in
   `.agents/skills/daisyui/components/` before writing code.
4. **View the current state** in
   `.review-screenshots/visual-polish/` (18 screenshots,
   gitignored locally). Regenerate via the procedure in that
   directory's README if needed.
5. **Run the test suite + css-check** to confirm a clean
   baseline:
   ```
   go test ./... -count=1
   golangci-lint run --timeout=5m
   make css-check
   ```
6. **Create a new branch for your work:**
   ```
   git checkout -b feat/ux-overhaul/<topic>   # e.g. feat/ux-overhaul/chat
   ```
   Each phase below is a separate branch + PR. Don't combine
   phases — keep them reviewable.

## Why this exists

A UX / graphical-designer review of the post-daisyUI UI found
~30 actionable improvements. They cluster into three groups:
- **Empty states** — currently afterthoughts; biggest
  new-user onboarding win
- **Message / interaction affordances** — chat messages lack
  avatars, timestamps, action buttons; destructive actions
  use inline checkboxes instead of modals
- **Cross-cutting infrastructure** — no toasts, no loading
  skeletons, no confirmation modals; the app silently swallows
  form submissions

The daisyUI component set is already in the bundle (60 KB
embedded via `go:embed`; see `internal/web/static/daisyui.min.css`).
The existing `internal/web/static/daisyui.min.css` is the
canonical asset; new template class names are picked up by the
`@source` scan in `static/src/daisyui.css` and a `make css`
rebuild folds them into the committed bundle.

## Scope and non-goals

**In scope:**
- Visual / interaction improvements using daisyUI components
  already in the bundle (`include:` list in
  `static/src/daisyui.css`; may add more if needed)
- Per-page empty-state and message styling overhauls
- Cross-cutting infrastructure (toasts, modals, skeletons,
  tooltips)
- Visual verification via headless-Chrome screenshots

**Out of scope (deferred projects):**
- New product features
- A theme picker (the OS-driven `prefers-color-scheme` is the
  final story for now)
- A complete visual redesign
- API additions (the ingest/search/documents APIs are
  unchanged)
- Mobile-native app

## Phasing (dependency-ordered)

The work is split into 6 phases. Each phase is a separate
branch + PR. Phases are ordered by dependency: each phase
builds on the previous without re-doing it.

```
Phase 1 (foundation, cross-cutting infra)
  │
  ├──> Phase 2 (chat — the app's main surface)
  │
  ├──> Phase 3 (search)
  │
  ├──> Phase 4 (documents)
  │
  └──> Phase 5 (login)

Phase 6 (cross-cutting polish — runs after the per-page work)
```

Phase 1 is required before any of 2–5 (they depend on the
toast/modal/skeleton infra). Phases 2–5 are independent of
each other and can be parallelized across instances. Phase 6
is the final pass.

---

## Phase 1 — Cross-cutting infrastructure

**Files affected:**
- `internal/web/static/chat.js` (extend with toast helper, modal
  helpers, auto-scroll)
- `internal/web/static/chat.css` (toast positioning, modal
  styling if not already covered by daisyUI; skeleton if used)
- `internal/web/templates/*.html` and
  `internal/web/fallback_renderers.go` (add toast container,
  modal container, skeleton placeholders where used)
- `internal/web/static/daisyui.min.css` (rebuild — picks up
  `skeleton`, `toast`, `modal`, `kbd` utility classes)

**Tests to add:**
- `TestUx_ToastContainerPresent` — every full page renders a
  `<div id="toast-container">` (or similar) at the end of
  `<body>`
- `TestUx_SkeletonClass` — at least one component renders
  `class="skeleton"` on its placeholder
- `TestUx_KeyboardShortcutKbd` — the chat form's help text
  uses daisyUI's `kbd` for the Ctrl/Cmd+Enter hint

**Visual verification:** `make css-check` + a fresh
screenshot of `/` and `/search` to confirm the toast
container and skeletons don't disrupt the page.

### 1.1 — Toast notification system

**Problem:** form submissions (delete, login error) silently
complete. Users have no feedback.

**Fix:** add a fixed-position toast container (`<div
id="toast-container" class="toast toast-top toast-end z-50">`)
on every full page. A small JS helper in `chat.js`
(`window.showToast(message, type)`) writes a daisyUI
`alert` with `class="alert alert-{type}"` into the container;
the alert auto-dismisses after 4s.

**daisyUI components:** `toast` (the container), `alert` (the
individual message; reuses the form-validation pattern from
the existing `internal/web/search_handlers.go` empty-query
case).

**HTML/CSS:** the container is empty in the static HTML; the
JS adds and removes children. The container is positioned
fixed at the top-right of the viewport (desktop) or full-width
at the top (mobile). daisyUI's `toast-top toast-end` classes
handle both.

**JS API:** `showToast(message, type, ttlMs)` where `type` is
`'success' | 'error' | 'warning' | 'info'`. Default ttl 4000ms.
The helper lives in `internal/web/static/chat.js`.

**Where it's called from:** document deletion (success and
error), chat message send (on error), login error (Phase 5).

### 1.2 — Modal component usage pattern

**Problem:** destructive actions (delete a document) currently
use an inline `<input type="checkbox" name="confirm">` + submit
flow. That's a Bulma-era pattern; the daisyUI equivalent is
`modal`.

**Fix:** establish a reusable modal pattern. The page renders
a `<dialog id="modal-{id}" class="modal">` element (initially
closed); a `data-modal-open="modal-{id}"` attribute on a button
opens it via `document.getElementById(id).showModal()`.

**daisyUI components:** `modal`, `modal-box`, `modal-action`,
`btn`, `btn-error`, `btn-ghost`. The form's submit button uses
`formmethod="dialog"` to close on submit (or the JS helper
calls `dialog.close()`).

**JS API:** a `window.openModal(id)` and `window.closeModal(id)`
helper pair. The form inside the modal is the actual mutation
form; the modal is just a presentation layer.

**Where it's used:** document delete (Phase 4), confirm
logout (future), anything else that needs user confirmation.

### 1.3 — Loading skeletons

**Problem:** pages show their empty state (`No documents
ingested yet`) even while the API is loading. Flashes the
wrong state on every page load.

**Fix:** when the server-rendered list is empty but the page
is "loading" (e.g., the URL has `?loading=1` or a JS flag is
set), render `<div class="skeleton h-4 w-full"></div>`
placeholders instead of the empty state. daisyUI's `skeleton`
component is a single class.

**Where it's used:** documents page while the API is loading
(skeleton rows in the table), search results while the
results fragment is loading (skeleton cards), chat log
placeholder while the page is hydrating (skeleton lines).

**Caveat:** the current server-side render returns the actual
empty state, not a loading state. Phase 4 is the right place
to introduce a real loading indicator on the documents page
(JS-driven via `htmx:afterRequest` or a manual
`fetch().then(...)`). For Phase 1, the skeleton class is
established; Phase 4 wires it up.

### 1.4 — `kbd` for keyboard shortcut hints

**Problem:** "Press Ctrl+Enter (Cmd+Enter on macOS) to send.
Enter inserts a newline." is a plain text string. Visual
distinction helps muscle memory.

**Fix:** replace the text with daisyUI `kbd`:
```html
Press <kbd class="kbd kbd-sm">Ctrl</kbd> +
<kbd class="kbd kbd-sm">Enter</kbd>
(Cmd+Enter on macOS) to send. Enter inserts a newline.
```

**daisyUI components:** `kbd`, `kbd-sm`.

**Where it's used:** chat form (the Ctrl/Cmd+Enter hint),
search form (the Enter to submit hint, if added).

---

## Phase 2 — Chat (the app's main surface)

**Files affected:**
- `internal/web/fallback_renderers.go` (chatFallbackBody;
  chat_message fragment is in a Go string)
- `internal/web/static/chat.css` (message spacing, avatar
  sizing, sticky navbar)
- `internal/web/static/chat.js` (auto-scroll, showToast on
  error, optional regenerate button handler)
- `internal/web/static/daisyui.min.css` (rebuild — picks up
  `avatar`, `kbd`, `chat-bubble` (if daisyUI has it),
  `loading`)

**Tests to add:**
- `TestChat_EmptyStateHasSuggestedPrompts` — the chat
  empty-state placeholder contains at least one
  clickable suggested-prompt button
- `TestChat_MessageHasAvatarAndTimestamp` — the chat
  message fragment renders `class="avatar"` and a `<time>`
  element
- `TestChat_NavbarIsSticky` — the chat fallback's body
  contains the `sticky` class on the header partial's
  rendered navbar wrapper
- `TestChat_StreamingIndicator` — the chat fallback
  includes a `class="loading loading-dots"` element (or
  similar) inside the htmx indicator wrapper
- `TestChat_KeyboardShortcutUsesKbd` — the chat form's
  help text uses `<kbd>` for the Ctrl/Cmd+Enter keys

**Visual verification:** capture `/` (chat landing) in
desktop and mobile, light and dark. The empty state should
show the suggested-prompts card. The chat-log area, when
populated, should show avatars + timestamps + action buttons
on hover.

### 2.1 — Suggested prompts in the empty state

**Problem:** new users don't know what to ask. The current
empty state is "Chat with your ingested documents. Type a
question below; each reply cites the chunks it was grounded
on." That's a description, not a call to action.

**Fix:** replace the empty-state `<div class="prose">` with a
centered card containing:
- An icon (the existing message-bubble SVG, slightly larger)
- A heading: "What would you like to know?"
- 2–3 example questions as `btn btn-ghost btn-block`
  buttons, each populating the textarea on click

**daisyUI components:** `card`, `card-body`, `btn`,
`btn-ghost`, `btn-block`, `kbd` (for the keyboard hint
inline).

**HTML structure:**
```html
<div class="prose flex flex-col items-center text-center"
     id="chat-messages-placeholder">
  <svg class="mb-3 size-12 opacity-50">…</svg>
  <p class="m-0 mb-4 text-base-content/60">
    What would you like to know?
  </p>
  <div class="flex flex-col gap-2 w-full max-w-md">
    <button class="btn btn-ghost btn-block justify-start"
            data-suggested-prompt="What does ADR-001 say about authentication?">
      <span class="opacity-60 mr-2">🔍</span>
      What does ADR-001 say about authentication?
    </button>
    <button class="btn btn-ghost btn-block justify-start"
            data-suggested-prompt="Summarize the migration plan">
      <span class="opacity-60 mr-2">🔍</span>
      Summarize the migration plan
    </button>
    <button class="btn btn-ghost btn-block justify-start"
            data-suggested-prompt="Find issues tagged security in GitLab">
      <span class="opacity-60 mr-2">🔍</span>
      Find issues tagged "security" in GitLab
    </button>
  </div>
  <p class="m-0 mt-3 text-xs text-base-content/60">
    Press <kbd class="kbd kbd-sm">Ctrl</kbd> +
    <kbd class="kbd kbd-sm">Enter</kbd> to send
  </p>
</div>
```

**JS (in `chat.js`):** a single delegated click handler on the
placeholder that finds the closest `[data-suggested-prompt]`
button, sets the textarea's value, focuses it, and removes the
placeholder (or hides it). When the user submits, the
placeholder is gone (the form was used).

**Hardcoded or configurable:** start with three hardcoded
prompts. A future change can read from a service endpoint.
The hardcoded set is the right default for a new install
(no docs to mine yet for dynamic suggestions).

### 2.2 — Message styling overhaul

**Problem:** chat messages are flat cards with no avatar, no
timestamp, no action bar, no metadata. Compare to Notion AI,
Claude.ai, ChatGPT — they all have:
- Operator / model avatar on the appropriate side
- Header row with name + timestamp
- Action bar (copy, regenerate, thumbs) on hover
- Citation badges inline instead of a collapsed `<details>`

**Fix:** redesign the chat message fragment (in
`internal/web/fallback_renderers.go` `chat_message.html` —
note: this is currently inline as a Go string literal, not
a separate file).

**daisyUI components:** `avatar`, `chat` (daisyUI has a
`chat` component specifically for chat-bubble layouts!),
`chat-bubble`, `badge`, `btn`, `btn-ghost`, `btn-xs`,
`btn-square`, `tooltip`, `kbd`.

**HTML structure (sketch):**
```html
<div class="chat chat-start">           <!-- operator on the left -->
  <div class="chat-image avatar">
    <div class="w-10 rounded-full bg-primary text-primary-content
                grid place-items-center">
      <span>Y</span>
    </div>
  </div>
  <div class="chat-header text-xs opacity-60">
    You · <time datetime="2026-10-03T23:00:00Z">just now</time>
  </div>
  <div class="chat-bubble chat-bubble-primary">
    <p class="prose">{{ .User }}</p>
  </div>
</div>
```

**daisyUI's `chat` + `chat-bubble` components** are built
exactly for this. Use them — don't reinvent.

**Action bar on the assistant bubble:**
```html
<div class="chat-footer mt-1 opacity-0 group-hover:opacity-100
            transition-opacity">
  <button class="btn btn-ghost btn-xs" data-action="copy">Copy</button>
  <button class="btn btn-ghost btn-xs" data-action="regenerate">↻</button>
  <span class="badge badge-info badge-sm"
        data-tooltip="3 chunks cited">3 sources</span>
</div>
```

The `group-hover:opacity-100` pattern (daisyUI's `group` +
`hover:` variants) is the standard chat-action-bar UX. The
sources badge uses the existing `data-tooltip` (via daisyUI
`tooltip`) for a hover-over detail.

**Source citations:** replace the existing `<details>` block
with inline `badge` chips. The daisyUI pattern:
```html
<div class="flex flex-wrap gap-1 mt-2">
  {{ range .Sources }}
  <a href="{{ .CitationURL }}" target="_blank" rel="noopener"
     class="badge badge-info badge-sm hover:badge-primary"
     data-tooltip="{{ .DocumentID }}">
    {{ .DisplayLabel }}
  </a>
  {{ end }}
</div>
```

**JS (in `chat.js`):** delegated handlers for the action
buttons. `data-action="copy"` writes the assistant's
`AnswerHTML` (plain-text-extracted) to the clipboard and
shows a toast. `data-action="regenerate"` re-POSTs to
`/chat/message` with the same message and replaces the
message bubble; if the current LLM doesn't support
regeneration, the button is hidden.

**Operator avatar:** the literal "Y" inside a colored circle
is the simplest path. A future change can swap for the OAuth
profile picture.

### 2.3 — Streaming / typing indicator

**Problem:** when the LLM is generating, the current design
shows a `class="badge">Thinking…` text. A more standard
pattern is a streaming indicator — three pulsing dots or a
spinner — that visually communicates "generating" without
needing text.

**Fix:** replace the `Thinking…` badge with a daisyUI
`loading` component:
```html
<div class="htmx-indicator" id="chat-indicator">
  <span class="loading loading-dots loading-md"></span>
</div>
```

**daisyUI components:** `loading`, `loading-dots`,
`loading-md`.

**The badge-to-loading swap is in the chat fallback body.**
The existing `.htmx-indicator` class is wired up by htmx
(`hx-indicator`); the loading component activates when the
indicator is shown and deactivates when hidden.

**Bonus:** the loading dots can be paired with a small
"Regenerating" or "Thinking..." text if the LLM is slow
(> 1.5s). Pure CSS — an `animation-delay` on the text
opacity.

### 2.4 — Sticky navbar

**Problem:** long chat sessions need persistent navigation.
The current navbar scrolls away with the page.

**Fix:** add `class="sticky top-0 z-10"` (Tailwind utilities)
to the navbar partial. daisyUI's `navbar` works fine as a
sticky element; the only adjustment is to give the body a
top padding equal to the navbar height (so content doesn't
disappear under the sticky bar).

**Where the change goes:** `internal/web/fallback_renderers.go`
`pageHeaderFallbackBody` — the `<nav class="navbar ...">`
gets `class="navbar bg-base-100 shadow-sm sticky top-0 z-10"`.
The `body` in each fallback needs `class="... pt-16"` (Tailwind
3.5rem ≈ navbar height).

**Caveat:** on the login page, the brand bar is not sticky
(it's a small element; sticky-ifying it would feel weird).
This change applies only to the pages that use the full
navbar (chat, search, documents).

### 2.5 — Auto-scroll behavior

**Problem:** new messages should auto-scroll to the bottom;
if the user has scrolled up, don't snap them down.

**Fix:** JS in `chat.js`. After every `htmx:afterRequest` on
`#chat-messages`, check if the user is "near the bottom" (within
~50px). If so, `scrollTop = scrollHeight`. If not, leave them
where they are.

**Why JS not a daisyUI feature:** scrolling behavior is
application logic, not styling. daisyUI has `scroll-snap` for
snapping, but the "near bottom" check is custom.

**The `Jump to latest` button** (already exists in the
post-migration chat) is the manual override. Phase 2
strengthens the auto-scroll and ensures the button still
appears when the user is scrolled away.

---

## Phase 3 — Search

**Files affected:**
- `internal/web/templates/search.html`
- `internal/web/templates/search_results.html`
- `internal/web/static/chat.css` (active filter chip styling,
  recent searches card)
- `internal/web/static/chat.js` (suggested-prompt click handler
  shared with chat; recent searches can be served from a
  localStorage cache)
- `internal/web/static/daisyui.min.css` (rebuild)

**Tests to add:**
- `TestSearch_ResultCountIsSelect` — the result count field
  is a `<select>` (not `<input type="number">`)
- `TestSearch_SubmitButtonIsPrimary` — the submit button has
  `class="btn btn-primary"` (not `btn-info`)
- `TestSearch_ActiveFiltersRenderChips` — when the search
  results fragment has filters applied, the active filter
  chips render as `class="badge"` elements
- `TestSearch_NoResultsHasAddDocumentCTA` — the no-results
  alert includes a link/CTA to the ingest guide
- `TestSearch_RecentSearchesOnEmpty` — when the form is
  empty and the user has previous searches, a "Recent
  searches" card renders below the form

**Visual verification:** `/search` (empty form, with results,
no-results) at desktop and mobile.

### 3.1 — Active filter chips with remove buttons (results state)

**Problem:** when a search has filters (tag, category,
document_id), the current design just shows the result count.
The user can't see what filters are active without scrolling
up to the form.

**Fix:** in `internal/web/templates/search_results.html`,
when filters are active, render a row of removable chips
above the results:
```html
{{ if .Filters }}
{{ if or .Filters.Tag .Filters.Category .Filters.DocumentID }}
<div class="mb-3 flex flex-wrap items-center gap-2">
  <span class="text-sm opacity-60">Active filters:</span>
  {{ if .Filters.Tag }}
  <a href="?query={{ .Query }}"
     class="badge badge-info gap-1 hover:badge-error">
    tag: {{ .Filters.Tag }} <span aria-hidden="true">×</span>
  </a>
  {{ end }}
  … (similar for Category, DocumentID)
</div>
{{ end }}
{{ end }}
```

The chip is a link to the same search with the filter removed
(preserves the other filters and the query). The
`hover:badge-error` provides a visual "removing" affordance.

**daisyUI components:** `badge`, `badge-info`, `gap-1` (for
the × spacing).

### 3.2 — Recent searches (empty state)

**Problem:** the empty search form has no helpful
suggestions. A user who has searched before would benefit
from their history.

**Fix:** below the form, when the form is empty (no current
query), render a "Recent searches" card with the user's last
5 queries. daisyUI's `menu` works:
```html
{{ if not .Query }}
{{ if .RecentSearches }}
<div class="card mt-4 max-w-md">
  <div class="card-body">
    <h3 class="card-title text-sm">Recent searches</h3>
    <ul class="menu">
      {{ range .RecentSearches }}
      <li><a href="?query={{ . }}">{{ . }}</a></li>
      {{ end }}
    </ul>
  </div>
</div>
{{ end }}
{{ end }}
```

**Storage:** the recent searches are read from a server-side
session store (or `localStorage` if you want a client-side
cache). The `?` is a design call — the new instance
should pick one and document it. The simplest first version:
in-memory `localStorage` keyed by a random per-user ID (since
ragabast is open-access by default).

### 3.3 — "Add document" CTA on no-results

**Problem:** the no-results state says "No results found for
this query." but offers no way to add data.

**Fix:** add a link to the ingest guide inside the no-results
alert:
```html
<div class="alert alert-warning flex flex-col items-center text-center">
  <svg …>…</svg>
  <p class="m-0">No results found for this query.</p>
  <p class="m-0 text-sm">
    Try removing the tag filter, or
    <a href="/docs#/operations/ingest" class="link link-warning">
      ingest a new document
    </a>.
  </p>
</div>
```

The `/docs#` link is the OpenAPI docs page (the project
already exposes one at `/.well-known/openapi.yaml` rendered
via Stoplight Elements).

### 3.4 — Result count as a `<select>`

**Problem:** the current `<input type="number" value="5"
min="1" max="50">` lets users type arbitrary values. The
form handler clamps them, but the UX invites confusion.

**Fix:** a `<select>` with 5 / 10 / 20 / 50:
```html
<select id="top_k" name="top_k" class="select select-sm w-20">
  <option value="5" {{ if eq .TopK 5 }}selected{{ end }}>5</option>
  <option value="10" {{ if eq .TopK 10 }}selected{{ end }}>10</option>
  <option value="20" {{ if eq .TopK 20 }}selected{{ end }}>20</option>
  <option value="50" {{ if eq .TopK 50 }}selected{{ end }}>50</option>
</select>
```

**daisyUI components:** `select`, `select-sm`.

### 3.5 — Sources descriptions

**Problem:** the Sources checkboxes just say "Docbuilder"
and "GitLab". No context.

**Fix:** one-line description under each label:
```html
<label class="flex items-start gap-2 cursor-pointer">
  <input type="checkbox" name="source_kinds" value="docbuilder"
         class="checkbox checkbox-sm mt-1"
         …>
  <div>
    <div class="font-medium">📄 Docbuilder</div>
    <div class="text-xs opacity-60">Synced from the docbuilder API</div>
  </div>
</label>
```

**daisyUI components:** standard `checkbox` + a stacked label
using `flex items-start` so the description wraps under the
name.

### 3.6 — Submit button as `btn-primary`

**Problem:** the Search button is `btn-info` (cyan).
Conventionally, the action CTA on a form uses `btn-primary`.

**Fix:** change `class="btn btn-info"` to `class="btn
btn-primary"` on the submit button. (The information
status, if used, stays `btn-info`; the form's action CTA
goes primary.)

**Why:** visual hierarchy. The primary action ("do the
search") is the main thing the user came to do.

---

## Phase 4 — Documents

**Files affected:**
- `internal/web/fallback_renderers.go` (documentsFallbackBody)
- `internal/web/templates/search.html` (the Documents link in
  the navbar; nothing to change there, but if you re-design
  the documents link, do it consistently)
- `internal/web/static/chat.css` (filter bar, table hover is
  already there from Phase 4 of visual-polish)
- `internal/web/static/chat.js` (modal helpers, clickable tag
  handlers, filter bar handler)
- `internal/web/static/daisyui.min.css` (rebuild — picks up
  `collapse`, `join`, `btn-circle`, `loading`)

**Tests to add:**
- `TestDocuments_DeleteUsesModal` — the documents page
  renders a `<dialog class="modal">` for confirmation; the
  inline `<input type="checkbox" name="confirm">` is gone
- `TestDocuments_FilterBarPresent` — the documents page has
  a search input at the top of the table
- `TestDocuments_ClickableTag` — the tag chips in the
  documents table are `<a>` elements (clickable, navigate
  to search with that tag pre-filled)
- `TestDocuments_PaginationUsesJoin` — the Previous/Next
  links are wrapped in a `class="join"` group
- `TestDocuments_EmptyStateHasCTA` — the empty state alert
  includes a "Get started" link

**Visual verification:** `/documents` empty, populated (with
hover), at desktop and mobile. (Populated requires ingesting
a document via the API; the screenshot harness can do this
with a bearer token.)

### 4.1 — "Add document" CTA on empty state

**Problem:** the empty state says "No documents ingested
yet" and references the API in code-form. There's no
in-page action.

**Fix:** add a primary CTA:
```html
<div class="alert flex flex-col items-center text-center">
  <svg …>…</svg>
  <p class="m-0">No documents ingested yet.</p>
  <p class="m-0">
    Submit one through <code>POST /api/ingest</code>
    or <a href="/docs#/operations/ingest"
          class="link link-primary font-medium">read the ingest guide →</a>
  </p>
</div>
```

**daisyUI components:** `alert` (existing), `link`,
`link-primary` (the daisyUI link-as-button pattern).

### 4.2 — Search / filter bar (populated state)

**Problem:** when the corpus has documents, there's no way
to filter the visible rows. The API supports `?q=`, but
there's no UI for it.

**Fix:** a search input above the table:
```html
<input type="search" name="q" value="{{ .Filter }}"
       class="input input-bordered w-full max-w-sm"
       placeholder="Filter by title, ID, tag, or category">
```

Filtering can be client-side (CSS `:has()` + `[hidden]`
toggles) or server-side (form action with `q` query param
that the handler respects). The simpler first version is
client-side: the input has an `oninput` handler that
iterates the table rows and toggles `[hidden]` based on
substring matches. The full server-side filter is a follow-up.

**daisyUI components:** `input`, `input-bordered`.

### 4.3 — Clickable tag chips

**Problem:** the tag chips in the documents table are static
`<span>`s. Clicking one would be useful — it should take the
user to `/search?tag=foo` with that filter applied.

**Fix:** change `<span class="badge badge-info">{{ . }}</span>`
to `<a href="/search?tag={{ . }}" class="badge
badge-info hover:badge-primary">{{ . }}</a>`. The
`hover:badge-primary` provides the visual "interactive"
affordance.

**Caveat:** the URL-encoding of `.` in `frontend.tags`
needs to be handled (`url.QueryEscape` or similar in the
template's template function set). The existing
`AssetURL` helper uses `url.QueryEscape`; reuse the pattern.

### 4.4 — Row details expansion

**Problem:** each row is a flat list of fields. Operators
managing documents may want to see the full frontmatter or
chunk count without leaving the page.

**Fix:** daisyUI's `collapse` component for an expand-on-click
details row:
```html
{{ range .Documents }}
<tr>
  <td>{{ .DisplayLabel }}</td>
  …
  <td>
    <button class="btn btn-ghost btn-xs"
            data-collapse="details-{{ .ID }}">Details</button>
  </td>
</tr>
<tr class="hidden" id="details-{{ .ID }}">
  <td colspan="7">
    <div class="collapse collapse-open">
      <div class="collapse-content">
        <dl class="grid grid-cols-2 gap-2 text-sm">
          <dt>UID</dt><dd><code>{{ .UID }}</code></dd>
          <dt>Path</dt><dd><code>{{ .FilePath }}</code></dd>
          <dt>Chunks</dt><dd>{{ .ChunkCount }}</dd>
          <dt>Created</dt><dd><time>{{ .CreatedAt }}</time></dd>
        </dl>
      </div>
    </div>
  </td>
</tr>
{{ end }}
```

**daisyUI components:** `collapse`, `collapse-content`,
`collapse-open` (or `collapse-arrow` for the chevron).

**JS (in `chat.js`):** delegated handler on
`[data-collapse]` that finds the target row and toggles its
`.hidden` class plus the `collapse-open` class on the
inner div.

### 4.5 — Modal-based delete (replace inline checkbox)

**Problem:** the current delete flow is `<input type="checkbox"
name="confirm" required>` + a `<button type="submit">Delete`
button. Two-step, easy to mis-click.

**Fix:** replace the inline checkbox with a modal trigger
button. Clicking opens a confirmation modal that contains
the actual delete form.

**HTML structure:**
```html
<button class="btn btn-ghost btn-xs text-error"
        data-modal-open="delete-{{ .ID }}">Delete</button>

<dialog id="delete-{{ .ID }}" class="modal">
  <div class="modal-box">
    <h3 class="text-lg font-bold">Delete document?</h3>
    <p class="py-4">
      "<strong>{{ .DisplayLabel }}</strong>" will be removed
      from the corpus. The source file on disk is not
      affected. This action cannot be undone.
    </p>
    <form method="post" action="/documents/{{ .ID }}/delete"
          class="modal-action">
      <input type="hidden" name="csrf_token" value="{{ $.CsrfToken }}">
      <button type="button" class="btn btn-ghost"
              data-modal-close="delete-{{ .ID }}">Cancel</button>
      <button type="submit" class="btn btn-error">Delete</button>
    </form>
  </div>
  <form method="dialog" class="modal-backdrop">
    <button>close</button>
  </form>
</dialog>
```

**daisyUI components:** `modal`, `modal-box`, `modal-action`,
`btn`, `btn-error`, `btn-ghost`.

**JS (in `chat.js`):** the modal helpers from Phase 1
(`openModal(id)` / `closeModal(id)`). Use them on the
`data-modal-open` / `data-modal-close` attributes.

**On success:** redirect to `/documents` and show a toast
("Document '<name>' deleted."). On error: show an error
toast with the server's message.

**Test update:** `TestDocumentsFallback_TableHeadersHaveScope`
and any test that pinned the inline `<input type="checkbox"
name="confirm">` will need updating.

### 4.6 — Pagination with `join`

**Problem:** the Previous / Next buttons are two separate
`btn btn-sm` links. No visual grouping.

**Fix:** wrap them in a daisyUI `join` group:
```html
<div class="join mt-4">
  {{ if gt .PrevOffset -1 }}
  <a class="join-item btn btn-sm"
     href="/documents?limit={{ .Limit }}&offset={{ .PrevOffset }}">
    Previous
  </a>
  {{ end }}
  {{ if gt .NextOffset -1 }}
  <a class="join-item btn btn-sm"
     href="/documents?limit={{ .Limit }}&offset={{ .NextOffset }}">
    Next
  </a>
  {{ end }}
</div>
```

**daisyUI components:** `join`, `join-item`, `btn`, `btn-sm`.

---

## Phase 5 — Login

**Files affected:**
- `internal/web/templates/login.html`
- `internal/web/static/login.css`
- `internal/web/static/daisyui.min.css` (rebuild)

**Tests to add:**
- `TestLogin_ProviderHasLogo` — each provider link includes
  an `<svg>` or `<img>` for the provider logo
- `TestLogin_HasErrorAlertTarget` — the page renders an
  empty `<div id="login-error" class="alert alert-error
  hidden">` container that JS shows on `?error=` query
  param
- `TestLogin_HasHelpLink` — the page renders a "Having
  trouble signing in? Contact your administrator" link
- `TestLogin_HasLogoMark` — the brand bar contains a small
  SVG mark (a stylized "r" or chat-bubble)

**Visual verification:** `/auth/login` (2+ providers) at
desktop and mobile, light and dark. The error state is
captured by adding `?error=oauth_failed` to the URL.

### 5.1 — Provider logos

**Problem:** the provider cards show only text. The standard
pattern (Auth0, Clerk, WorkOS) is to show the provider's
logo.

**Fix:** add inline SVGs for each provider. GitHub's
Octocat and GitLab's Tanuki are well-known marks; they're
both available as small SVGs (search GitHub/GitLab's brand
assets; both are MIT-licensed and ship in many auth UIs).

**HTML structure:**
```html
<a href="/auth/gh/login..."
   class="provider-link flex items-center gap-3">
  <svg class="size-5" viewBox="0 0 24 24" fill="currentColor"
       aria-hidden="true">
    <path d="M12 .5C5.65.5.5 5.65.5 12c0 5.08 3.29 9.39 7.86 10.91.58.11.79-.25.79-.56 0-.29-.01-1.06-.02-2.08-3.2.69-3.87-1.39-3.87-3.14 0-1.04.37-1.89.98-2.27-.1-.25-.43-1.22.1-2.55 0 0 .8-.26 2.62 1 .76-.21 1.58-.32 2.4-.32.82 0 1.64.11 2.4.32 1.82-1.26 2.62-1 2.62-1 .53 1.33.2 2.3.1.61.38.98 1.23.98 2.27 0 1.75-.67 2.45-1.87.65 1.15.2 2.39.01 2.55.67.84.2 1.85-.32 2.62 0 1.04-.01 1.79-.01 2.05 0 .31.21.7.67.55C20.71 21.39 24 17.08 24 12c0-6.35-5.15-11.5-11.5-11.5z" />
  </svg>
  <span>Sign in with GitHub</span>
</a>
```

**Caveat:** the SVGs go in `internal/web/templates/login.html`
directly. The `internal/web/static/login.css` doesn't need
changes for the icons themselves; the `flex items-center
gap-3` on the link handles the layout. The icon color uses
`fill="currentColor"` so the link's `text-[#e6edf3]` sets the
icon color (a subtle but important detail — the icons follow
the text color in dark mode).

**Per-provider SVG paths:** look them up at implementation
time. The daisyUI skill's `avatar` component guide has
example SVG snippets for major providers (the daisyUI
ecosystem's login examples use real GitHub/GitLab SVGs).

### 5.2 — Error state

**Problem:** when OAuth fails (user denies access, callback
error, expired state), the current page has no visible error.

**Fix:** the login page renders an alert container that's
empty by default. A new instance of `login.html` is
expected to support a `?error=oauth_failed` query param;
the server-side handler sets a context field that the
template renders into the alert:
```html
<div id="login-error"
     class="alert alert-error flex items-center gap-2 {{ if not .Error }}hidden{{ end }}">
  <svg …>…</svg>
  <span>{{ .Error }}</span>
</div>
```

**daisyUI components:** `alert`, `alert-error`.

**Server-side work:** the OAuth callback handler should
redirect to `/auth/login?error=oauth_failed` (or similar
specific codes) on failure. The current handler probably
already returns 4xx; the fix is to add a query-param redirect
on those failures.

### 5.3 — Help link

**Fix:** a small "Having trouble signing in?" link below
the chooser card:
```html
<p class="m-0 mt-4 text-center text-[0.8rem] text-[#7d8590]">
  Having trouble signing in?
  <a href="mailto:admin@example.com" class="link link-hover:link-primary">
    Contact your administrator
  </a>
</p>
```

(The mailto address is a placeholder; the real instance
should read it from a config field like `operator_email`.)

**daisyUI components:** `link`, `link-hover:link-primary`
(Tailwind hover variant on the daisyUI `link` base class).

### 5.4 — Small logo / mark

**Fix:** add a small inline SVG to the brand bar (just before
the "ragabast" text):
```html
<header class="brand-bar">
  <a href="/" class="brand flex items-center gap-2">
    <svg class="size-5 text-base-content"
         viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
      <path d="…" />  <!-- a simple speech-bubble or "r" mark -->
    </svg>
    <span>ragabast</span>
  </a>
</header>
```

The SVG is a small (~24x24) speech-bubble or stylized "r"
mark in `currentColor`. The exact path is a design call; the
new instance should pick something simple. (A square with
"r" inside is the minimum; a chat-bubble shape is more
on-brand.)

---

## Phase 6 — Cross-cutting polish

**Files affected:**
- `internal/web/static/chat.js` (toast integration, tooltip
  wiring, focus state reinforcement)
- `internal/web/static/chat.css` (kbd + tooltip + skeleton
  styling)
- `internal/web/static/daisyui.min.css` (rebuild — picks up
  `kbd`, `tooltip`, `skeleton`, `loading` utility classes)
- Per-page templates where these utilities are applied (see
  per-phase items above)

**Tests to add:**
- `TestUx_FocusStateGloballyVisible` — every focusable
  element has either a daisyUI focus class or the explicit
  `focus-visible:outline-2` in chat.css
- `TestUx_TooltipPresent` — at least one icon-only button
  has a `data-tooltip` attribute (or daisyUI's `tooltip`
  pattern)

**Visual verification:** every page in light + dark +
desktop + mobile. The focus rings should be visible (and
similar) across every page.

### 6.1 — Better empty state copy

A copy pass on every empty state, using a tone that's
helpful rather than informational:

| Where | Current | Proposed |
|---|---|---|
| Chat | "Chat with your ingested documents. Type a question below; each reply cites the chunks it was grounded on." | "What would you like to know?" + suggested prompts |
| Documents | "No documents ingested yet. Submit one through `POST /api/ingest` to add some." | "No documents yet." + "Submit one through `POST /api/ingest`" + "or read the ingest guide →" |
| Search no-results | "No results found for this query." | "No matches." + "Try removing the tag filter, or ingest a new document →" |
| Login (success path) | (no message) | Keep silent; the redirect to / handles it |

The exact wording is a design call. The new instance
should write a tone-of-voice that's consistent across all
empty states ("What would you like to..." for new-user
guidance; "Nothing here yet" for empty data; "No matches"
for failed searches).

### 6.2 — Improved focus states (reinforce)

The visual-polish branch already added explicit focus rings
on the login page's custom elements. Phase 6 ensures
consistency across every page:

- daisyUI components have built-in focus rings. Don't
  override them with `focus:outline-none`.
- Custom elements (the brand link, the provider links, the
  table row hover indicator, the empty-state SVG) get
  explicit `focus-visible:outline-2 focus-visible:outline-primary
  focus-visible:outline-offset-2` Tailwind utility classes.
- The skip link has its own focus style (already in chat.css
  and login.css from visual-polish).

**The new instance should grep the templates for
`focus:outline-none` and remove any occurrences.** They're
a11y antipatterns (the original Bulma-era "the border-color
changes on focus" trick).

### 6.3 — Tooltip on icon-only buttons

**Problem:** the delete button (now a modal trigger) and the
jump-to-latest button have no visible label.

**Fix:** daisyUI's `tooltip` (CSS-based, no JS):
```html
<button class="btn btn-circle btn-ghost"
        data-tooltip="Jump to latest"
        aria-label="Jump to latest message">
  ↓
</button>
```

**daisyUI components:** `tooltip`, `btn-circle`, `btn-ghost`.

**Where:** jump-to-latest button (chat), suggested-prompt
emoji icons (chat), maybe the source-kind emojis (search).
The delete button is no longer icon-only (Phase 4 makes it
a "Delete" text button), so it doesn't need a tooltip.

### 6.4 — Loading skeletons (wired up)

Phase 1 introduced the `skeleton` class. Phase 6 wires it
up where data is loading:
- The documents table renders skeleton rows while the API
  is in-flight
- The search results fragment renders skeleton cards while
  the search is running
- The chat log placeholder uses skeleton lines briefly
  during initial load

**Approach:** a tiny inline script in each page that toggles
a `class="hidden"` on the actual content vs the skeleton
container. Or: htmx's `hx-indicator` + a corresponding
`htmx-request` class on the page that toggles skeleton
visibility.

---

## Acceptance criteria (whole plan)

When all 6 phases are complete, every box below must be
checked.

1. ✓ Chat empty state shows 2–3 clickable suggested prompts
   (not just a sentence)
2. ✓ Chat messages render with avatar, name, timestamp,
   hover-revealed action bar (copy + regenerate + sources
   badge)
3. ✓ Chat shows a streaming/typing indicator while the LLM
   is generating
4. ✓ Chat navbar is sticky and the body content doesn't
   disappear under it
5. ✓ Search form's result count is a `<select>` (not
   `<input type="number">`)
6. ✓ Search submit is `btn-primary`
7. ✓ Search results show removable active-filter chips
   above the results
8. ✓ Search no-results state includes an "ingest more
   documents" CTA
9. ✓ Documents page empty state has a primary "Ingest a
   document" CTA
10. ✓ Documents page has a client-side filter bar above
    the table
11. ✓ Documents page tag chips are clickable links to
    pre-filled search
12. ✓ Documents page delete uses a modal confirmation
    (not an inline checkbox)
13. ✓ Documents page pagination uses `join`
14. ✓ Login page shows provider logos next to the names
15. ✓ Login page renders an error state when `?error=` is
    present
16. ✓ Login page has a "Contact your administrator" help
    link
17. ✓ Login page brand bar has a small logo mark
18. ✓ All forms show toast notifications on success / error
19. ✓ Skeleton loaders appear briefly while data is loading
20. ✓ All empty states have icon + helpful copy (not just
    text)
21. ✓ All interactive elements have a visible focus
    indicator (daisyUI defaults + custom rules)
22. ✓ Icon-only buttons have tooltips
23. ✓ `go test ./...` passes
24. ✓ `golangci-lint run` is clean
25. ✓ `make css-check` is fresh

## Files affected (consolidated)

| File | Phase(s) | What changes |
|---|---|---|
| `internal/web/fallback_renderers.go` | 2, 4, 6 | chat message fragment redesign, documents empty state CTA, documents pagination with `join`, sticky navbar on chat |
| `internal/web/templates/login.html` | 5 | provider logos, error state, help link, small logo mark |
| `internal/web/templates/search.html` | 3 | result count as select, sources descriptions, submit button → `btn-primary`, recent searches card |
| `internal/web/templates/search_results.html` | 3 | active filter chips with remove buttons, sources as badges, "add document" CTA on no-results |
| `internal/web/static/login.css` | 5 | styling for new elements (logos, alert, help link) |
| `internal/web/static/chat.css` | 1, 2, 3, 4, 6 | skip link (Phase 1 polish, already there), table hover (Phase 4 polish, already there), kbd, skeleton, tooltip, recent-searches card, active filter chips |
| `internal/web/static/chat.js` | 1, 2, 3, 4, 6 | toast helper, modal helpers, suggested-prompt click handler, clickable-tag handler, delete-modal handler, auto-scroll, tooltip wiring |
| `internal/web/static/daisyui.min.css` | every | rebuilt via `make css` after every phase |
| `internal/web/static_test.go` | 1, 2, 3, 4, 5, 6 | new tests per phase |
| `internal/web/chat_landing_test.go` | 2 | updated for suggested prompts and message styling |
| `internal/web/documents_chips_test.go` | 4 | updated for the modal delete (no more inline checkbox) |

## How to start a new instance (cheat sheet)

```bash
# 1. Check out the spec branch
git checkout feat/ux-overhaul

# 2. Create a work branch for your phase
git checkout -b feat/ux-overhaul/chat   # example for Phase 2

# 3. Load the daisyUI skill (required for every component reference)
# Open .agents/skills/daisyui/SKILL.md and the relevant component guide
# before writing any HTML.

# 4. Run baseline tests
go test ./... -count=1
make css-check

# 5. Make CSS-aware changes. The daisyUI bundle is committed at
#    internal/web/static/daisyui.min.css. After every change that
#    introduces new utility classes, run:
make css
# to rebuild and commit the new daisyui.min.css.

# 6. Add tests in TDD style:
#    - red: write the test, confirm it fails
#    - green: make the production change
#    - refactor: clean up

# 7. Take screenshots to verify visually:
#    - use /tmp/screenshot.mjs (1280x900) and /tmp/screenshot-mobile.mjs (390x844)
#    - both use Chrome DevTools Protocol's Emulation.setEmulatedMedia
#    - the light/dark flag is the third arg
#    - save to .review-screenshots/ux-overhaul/<phase>/<page>-<viewport>.png
#    - capture before AND after to see the diff

# 8. Commit with conventional commits:
#    - feat(web): <what you did>
#    - the commit body should reference this SPEC
#    - mention the daisyUI component(s) used
#    - mention the test(s) added/updated

# 9. Push, open a PR, request review. The PR should be ONE
#    phase (don't combine phases).
```

## What NOT to do

- **Don't combine phases.** Each phase is a separate PR. The
  reviewer can sign off on Phase 1 (infra) before Phase 2
  (chat) is even started.
- **Don't add new pages or product features.** This is a
  visual / interaction pass on the existing surface.
- **Don't re-introduce `focus:outline-none`.** It was a
  Bulma-era antipattern. daisyUI components have their own
  focus styles; custom elements get explicit `focus-visible`
  classes.
- **Don't add Tailwind utility classes for things daisyUI
  has components for.** daisyUI's `btn`, `card`, `modal`,
  `tabs`, `menu`, `join`, `kbd`, `skeleton`, `tooltip`, `toast`
  all exist. Use them.
- **Don't skip the CSS rebuild.** After adding new utility
  classes, `make css` picks them up via the @source scan
  and folds them into daisyui.min.css. If you forget,
  `make css-check` fails in CI.
- **Don't take shortcuts on the test side.** The
  daisyUI-shape contracts from the original migration
  (TestChatFallback_UsesDaisyUIComponents, etc.) are still
  the contract. If you change a class name, update the test
  in the same commit. Don't accumulate test debt.
- **Don't refactor the CSS architecture.** The current
  setup (committed daisyui.min.css + chat.css + login.css)
  is intentional. If you want a different split, propose it
  in a separate commit *before* starting the relevant phase.

## Visual verification checklist (per phase)

For each phase, take screenshots before AND after the change.
Save to `.review-screenshots/ux-overhaul/<phase>/`. The
README in that directory should describe what each
screenshot shows and why.

Phase 2 (chat) — the most important:
- Before: 18 screenshots showing the current chat
  (post-visual-polish)
- After: 18 screenshots showing the new chat (suggested
  prompts in empty state, polished message styling, sticky
  navbar, streaming indicator, kbd shortcut)

Phase 3 (search):
- Search empty form, with results, no-results — desktop
  and mobile, light and dark
- Before/after pairs

Phase 4 (documents):
- Documents empty, populated (with hover), with delete
  modal open — desktop and mobile
- Pagination visible at the bottom

Phase 5 (login):
- Login normal, login with `?error=oauth_failed` —
  desktop and mobile, light and dark
- Provider logos visible

Phase 1 + 6 (cross-cutting):
- Toast notification visible (e.g., after a delete)
- Skeleton loader visible (e.g., documents page during load)
- Focus state visible (Tab through a page; capture mid-tab)
- kbd element visible (chat form help text)
- Tooltip visible (hover over the jump-to-latest button)

The screenshots live in `.review-screenshots/ux-overhaul/`
(gitignored). They are the source of truth for "does this
look right?" — when in doubt, screenshot it.

---

## Estimated effort

| Phase | Effort | Depends on |
|---|---|---|
| 1 — infrastructure | 0.5–1 day | (none — foundation) |
| 2 — chat | 1.5–2 days | Phase 1 |
| 3 — search | 0.5–1 day | Phase 1 |
| 4 — documents | 1 day | Phase 1 |
| 5 — login | 0.5 day | Phase 1 |
| 6 — cross-cutting polish | 0.5 day | Phases 1–5 |
| 7 — prose typography | 0.5 day | Phases 2, 3 (template + CSS) |
| 8 — login theme follow-on | 0.5 day | Phase 5 |

**Total: ~5–6 working days** for a single instance, working
top-to-bottom. Phases 2–5 can be parallelized across
instances; the dependency is only on Phase 1.

---

## Phase 7 — prose typography follow-on

A late-arriving regression: the markdown the LLM emits has
been correctly rendered as `<ul>`/`<ol>`/`<li>`/`<p>`/`<h*>`/`<pre>`
since the original markdown pipeline (goldmark + GFM) was
introduced, but the supporting typography plugin
(`@tailwindcss/typography`) was never installed. The chat
assistant reply, the search-result chunk bodies, and the
landing placeholder all carry `class="prose"` as the
contract for "render markdown-shaped content with proper
typography" — but the `.prose` selectors in the shipped
bundle were empty, so the browser fell back to UA defaults.

Concretely, this showed up as:

- Lists rendering with raw browser defaults — 40px left
  margin, a small bullet character, no spacing between
  items, no line-height harmony with surrounding text.
- Paragraphs not getting vertical rhythm (each `<p>` flush
  against the next, no margin-block).
- Headings (`<h2>`, `<h3>`) inheriting the body text size,
  not the typography plugin's larger scale.
- Code blocks not getting the plugin's background color
  or padding.

The Bulma 1.x era had its own `.content` reset that shipped
the same rules; the daisyUI migration swapped that for
`.prose` but the typography plugin install step was missed.

### Files

| File | What changes |
|---|---|
| `package.json` | Add `@tailwindcss/typography` to `devDependencies`. |
| `static/src/daisyui.css` | Add `@plugin "@tailwindcss/typography";` after `@source` and before `@plugin "daisyui"`. |
| `internal/web/templates/chat_message.html` | Apply `prose prose-sm` (instead of just `prose`) to the assistant's reply `<div class="chat-msg">`. prose-sm (0.875rem / 14px) is chat-appropriate; prose-base (1rem / 16px) is too large inside the chat-bubble. |
| `internal/web/static/chat.css` | Add a scoped override: `.chat-msg.prose { color: inherit }`. The plugin defaults `.prose` body text to `--tw-prose-body` (a hardcoded gray); inside a chat-bubble that gray overrides the bubble's themed color. The override keeps the plugin's intent everywhere else (search results, landing) but restores the chat-bubble color where it matters. |
| `internal/web/static/daisyui.min.css` | Rebuilt via `make css`. Bundle grows by ~24 KB (85 KB → 110 KB), still under the 200 KB Phase 7 budget and the 250 KB existing `static_test.go` budget. |
| `internal/web/chat_prose_test.go` (new) | Six tests pinning the contract (described below). |

### Tests (in `internal/web/chat_prose_test.go`)

1. `TestChatMessageFragment_AssistantReplyUsesProseSm` —
   pins that the assistant reply `<div>` carries
   `chat-msg prose prose-sm`. The size modifier is the
   load-bearing visual contract.
2. `TestChatMessageFragment_OperatorMessagePlainText` —
   pins that the operator message stays inside
   `<pre class="chat-msg">` (no `prose`, no markdown). The
   operator types raw text; only the assistant reply is
   markdown-rendered.
3. `TestChatMessageFragment_HandlesListAndParagraphMarkdown`
   — pins that the chat handler end-to-end renders
   `- one` / `- two` markdown as `<ul><li>one</li><li>two</li></ul>`
   and wraps a paragraph in `<p>...</p>`. The search-side
   `TestHandleSearchSubmit_RendersLists` covers the
   renderer; this pins the chat handler uses it.
4. `TestChatMessageFragment_HandlesHeadingAndCodeBlock` —
   same shape of pin for `## heading` and fenced
   ```code blocks```.
5. `TestShippedCSS_TypographyPluginLoaded` — pins that
   the shipped `daisyui.min.css` contains the plugin's
   `.prose :where(ul|ol|p|h2|h3|pre|code):not(...)` selectors.
   Without this the prose class is a hollow contract.
6. `TestShippedCSS_ChatMsgProseInheritsColor` — pins that
   `chat.css` carries `.chat-msg.prose { color: inherit }`.
7. `TestShippedCSS_BundleSizeBudgetReasonable` — tighter
   Phase-7-specific bundle budget: 200 KB (vs. the
   existing 250 KB test). A runaway plugin upgrade is
   caught immediately.

### Acceptance criteria

When Phase 7 is complete, every box below must be checked:

1. ✓ `npm install` adds `@tailwindcss/typography` to
   `package-lock.json`
2. ✓ `static/src/daisyui.css` carries
   `@plugin "@tailwindcss/typography";`
3. ✓ `internal/web/templates/chat_message.html` carries
   `class="chat-msg prose prose-sm"` on the assistant
   reply div
4. ✓ `internal/web/static/chat.css` carries
   `.chat-msg.prose { color: inherit }`
5. ✓ `internal/web/static/daisyui.min.css` includes
   `.prose :where(ul|ol|li|p|h2|h3|pre|code):not(...)`
6. ✓ All six new tests in `chat_prose_test.go` pass
7. ✓ All existing tests pass (`go test ./... -count=1`)
8. ✓ `golangci-lint run` is clean
9. ✓ `make css-check` is fresh
10. ✓ daisyui.min.css stays under 200 KB
11. ✓ Visual verification: chat reply with a list shows
    properly indented bullet items with comfortable
    spacing, in both light and dark mode

---

## Phase 8 — login theme follow-on

A late-arriving regression from Phase 5 (login overhaul).
The Phase 5 changes added daisyUI utility classes to
`templates/login.html` (`flex items-center gap-3
border ... size-5 shrink-0 ...`) but never added
`<link rel="stylesheet" href=".../daisyui.min.css">`
to the login page's `<head>`. Result: every daisyUI
utility class on the login page was a hollow contract.

Concrete visible symptoms on the rendered page:

1. The provider SVGs (GitHub Octocat, GitLab Tanuki)
   rendered at intrinsic viewBox dimensions, dwarfing
   the page — the brand mark SVG also rendered at
   intrinsic size.
2. The brand bar (navbar) had no flex layout because
   `navbar` / `navbar-start` / `navbar-end` /
   `bg-base-100` didn't apply.
3. The provider buttons were mis-positioned because
   `flex items-center gap-3` didn't apply.

Plus a separate theme issue: the page used a
hardcoded GitHub-dark palette (`text-[#8b949e]`,
`bg-[#161b22]`, `border-[#30363d]`, etc.) which
was the pre-Phase-5 design pinned via `body {
background: #0e1116; color: #e6edf3; }` in
`login.css`. The page rendered the same dark
regardless of `prefers-color-scheme`.

### Fix

| File | What changes |
|---|---|
| `internal/web/templates/login.html` | Add `<link rel="stylesheet" href="{{ asset "daisyui.min.css" }}">` to `<head>`. Add `class="bg-base-100 text-base-content"` to `<body>`. Replace six hardcoded GitHub-dark tokens (`border-[#30363d]`, `bg-[#161b22]`, `text-[#e6edf3]`, `text-[#8b949e]`, `text-[#6e7681]`, `text-[#7d8590]`, plus `hover:border-[#58a6ff]` / `focus:border-[#58a6ff]` / `focus-visible:outline-[#58a6ff]`) with daisyUI theme-aware classes (`border-base-300`, `bg-base-200`, `text-base-content`, `text-base-content/70`, `text-base-content/60`, `text-base-content/50`, `hover:border-primary` / `focus:border-primary` / `focus-visible:outline-primary`). |
| `internal/web/static/login.css` | Drop the hardcoded `body { background: #0e1116; color: #e6edf3 }` block. The body now inherits daisyUI's themed `bg-base-100` / `text-base-content` from the template. |
| `static/src/daisyui.css` | Add `link` to the daisyUI `@plugin "daisyui" { include: ... }` list. The pre-Phase-8 template used `link link-hover:link-primary` (daisyUI v4 / Tailwind v3 syntax) which silently dropped to a raw anchor under daisyUI v5. Phase 8 also updates the template to the daisyUI v5 syntax `link link-hover link-primary`. |
| `internal/web/static/daisyui.min.css` | Rebuilt via `make css`. Bundle grows by ~824 bytes (the `link` component). |
| `internal/web/login_themes_test.go` (new) | Four tests pinning the contract (see below). |
| `internal/web/static_test.go` | Two existing tests pinned the pre-Phase-8 hardcoded-palette contract; both are updated to the new theme-aware contract. `TestStaticHandler_ServesLoginCSS` flips from `require.Contains(body, "#0e1116")` to `require.NotContains(body, "#0e1116")`. `TestLoginTemplate_UsesTailwindUtilities` swaps `text-[#8b949e]` and `text-[#6e7681]` for the daisyUI equivalents. |

### Tests (in `internal/web/login_themes_test.go`)

1. `TestLogin_LoadsDaisyUIBundle` — pins that
   `/auth/login` HTML carries
   `<link rel="stylesheet" href="/static/daisyui.min.css">`.
   Without this, every daisyUI utility class on the
   page is a hollow contract.
2. `TestLogin_NoHardcodedGitHubDarkTokens` — pins that
   none of the six pre-Phase-8 hex tokens appear in the
   rendered HTML body (comments stripped first; the
   Go html/template engine preserves the inner text
   of a `<!-- ... -->` comment in some cases).
3. `TestLogin_UsesThemeAwareClasses` — positive
   counterpart: the page carries `bg-base-100` and
   `text-base-content` so it follows daisyUI's theme.
4. `TestLoginCSS_NoHardcodedBodyBackground` — pins
   that `login.css` no longer pins `body { background:
   #0e1116; color: #e6edf3 }`. Comments stripped first.

### Acceptance criteria

When Phase 8 is complete, every box below must be
checked:

1. ✓ `internal/web/templates/login.html` loads
   `daisyui.min.css`
2. ✓ `login.html` `<body>` carries
   `class="bg-base-100 text-base-content"`
3. ✓ `login.html` uses no hardcoded GitHub-dark hex
   tokens (`#30363d`, `#161b22`, `#e6edf3`, `#8b949e`,
   `#6e7681`, `#7d8590`)
4. ✓ `login.html` uses daisyUI theme-aware classes
   (`bg-base-200`, `text-base-content`, `border-base-300`,
   `text-base-content/70`, `text-base-content/60`,
   `text-base-content/50`)
5. ✓ `login.html` uses the daisyUI v5 link syntax
   (`link link-hover link-primary`, not the v4
   `link link-hover:link-primary`)
6. ✓ `static/src/daisyui.css` `@plugin "daisyui"`
   include list contains `link`
7. ✓ `internal/web/static/login.css` body block does
   NOT pin `background:` or `color:`
8. ✓ All four new tests in `login_themes_test.go` pass
9. ✓ The two updated tests in `static_test.go` pass
10. ✓ All existing tests pass (`go test ./... -count=1`)
11. ✓ `golangci-lint run` is clean
12. ✓ `make css-check` is fresh
13. ✓ Visual verification: provider SVGs render at 20×20,
    navbar lays out correctly with the brand mark on the
    left and the Chat/Search/Documents links on the right,
    provider buttons sit inside the card with proper flex
    alignment, in both light and dark mode

## Why this is the right next step

The daisyUI migration was about getting the project onto a
maintained framework. The visual-polish branch was the first
sweep of a11y and visual cleanup. This UX-overhaul is the
*product* polish — the work that makes the app feel like a
real product, not a CLI wrapper with a web UI.

The daisyUI component set is already in the bundle. The CSS
infrastructure is in place. The test contracts are pinned.
This plan is the bridge from "the daisyUI migration is done"
to "the daisyUI migration is done *well*."

When a new instance picks this up, they should be able to
execute end-to-end without further design decisions: every
item has a daisyUI component choice, a file location, a
test contract, and an acceptance criterion.
