# UX overhaul — starting checklist for a new instance

A short checklist version of `plans/ux-overhaul.md` for the
new instance that picks this up. The full SPEC is the source
of truth; this is the boot sequence.

## 1. Load the context

```bash
# Already on the branch
git status  # confirm clean

# Skim, don't re-read
cat plans/ux-overhaul.md

# The daisyUI skill (read the components you need)
ls .agents/skills/daisyui/components/

# The current state
ls .review-screenshots/visual-polish/ | head -20
```

## 2. Create your work branch

```bash
# For Phase 1 (foundation)
git checkout -b feat/ux-overhaul/foundation

# For Phase 2 (chat)
# git checkout -b feat/ux-overhaul/chat

# Etc. One phase = one branch = one PR.
```

## 3. Run baseline

```bash
go test ./... -count=1
golangci-lint run --timeout=5m
make css-check
```

## 4. Phase-by-phase

### Phase 1 — Foundation (do this first)

**Files:**
- `internal/web/static/chat.js` (toast helper, modal helpers,
  auto-scroll)
- `internal/web/static/chat.css` (toast positioning, kbd,
  skeleton if used in CSS)
- `internal/web/templates/*.html` and
  `internal/web/fallback_renderers.go` (toast container,
  modal containers)

**Sub-tasks:**
- [ ] 1.1 Toast notification system (`<div id="toast-container">`
  on every full page, `window.showToast(msg, type)` helper in
  chat.js)
- [ ] 1.2 Modal component usage pattern (`<dialog
  class="modal">` with `data-modal-open` / `data-modal-close`
  attributes, helper JS)
- [ ] 1.3 Loading skeletons (class established; wired up in
  Phase 4)
- [ ] 1.4 `kbd` for keyboard shortcut hints (chat form's
  Ctrl/Cmd+Enter)

**Tests to add:**
- `TestUx_ToastContainerPresent` (every full page)
- `TestUx_SkeletonClass` (at least one usage somewhere)
- `TestUx_KeyboardShortcutKbd` (chat form)

### Phase 2 — Chat (depends on Phase 1)

**Files:** `fallback_renderers.go` (chatFallbackBody and
chat_message), `chat.css`, `chat.js`, `daisyui.min.css`

**Sub-tasks:**
- [ ] 2.1 Suggested prompts in the empty state (the big win)
- [ ] 2.2 Message styling overhaul (avatar, timestamp, action
  bar, sources as badges)
- [ ] 2.3 Streaming / typing indicator (`loading loading-dots`)
- [ ] 2.4 Sticky navbar (`class="sticky top-0 z-10"` on
  navbar, `pt-16` on body)
- [ ] 2.5 Auto-scroll behavior (JS in chat.js; near-bottom
  detection)

**Tests to add:**
- `TestChat_EmptyStateHasSuggestedPrompts`
- `TestChat_MessageHasAvatarAndTimestamp`
- `TestChat_NavbarIsSticky`
- `TestChat_StreamingIndicator`
- `TestChat_KeyboardShortcutUsesKbd` (also from Phase 1)

### Phase 3 — Search (depends on Phase 1)

**Files:** `templates/search.html`, `templates/search_results.html`,
`chat.css`, `chat.js`, `daisyui.min.css`

**Sub-tasks:**
- [ ] 3.1 Active filter chips with remove buttons (results state)
- [ ] 3.2 Recent searches (empty form state, localStorage
  or session-based)
- [ ] 3.3 "Add document" CTA on no-results state
- [ ] 3.4 Result count as a `<select>`
- [ ] 3.5 Sources descriptions
- [ ] 3.6 Submit button as `btn-primary`

**Tests to add:**
- `TestSearch_ResultCountIsSelect`
- `TestSearch_SubmitButtonIsPrimary`
- `TestSearch_ActiveFiltersRenderChips`
- `TestSearch_NoResultsHasAddDocumentCTA`
- `TestSearch_RecentSearchesOnEmpty`

### Phase 4 — Documents (depends on Phase 1)

**Files:** `fallback_renderers.go` (documentsFallbackBody),
`chat.css`, `chat.js`, `daisyui.min.css`

**Sub-tasks:**
- [ ] 4.1 "Add document" CTA on empty state
- [ ] 4.2 Search / filter bar (populated state, client-side
  filtering)
- [ ] 4.3 Clickable tag chips (link to `/search?tag=foo`)
- [ ] 4.4 Row details expansion (`collapse` component)
- [ ] 4.5 Modal-based delete (replace inline checkbox; use
  the modal helpers from Phase 1)
- [ ] 4.6 Pagination with `join`

**Tests to add:**
- `TestDocuments_DeleteUsesModal`
- `TestDocuments_FilterBarPresent`
- `TestDocuments_ClickableTag`
- `TestDocuments_PaginationUsesJoin`
- `TestDocuments_EmptyStateHasCTA`

### Phase 5 — Login (depends on Phase 1)

**Files:** `templates/login.html`, `static/login.css`,
`daisyui.min.css`

**Sub-tasks:**
- [ ] 5.1 Provider logos (inline SVGs in the provider links)
- [ ] 5.2 Error state (`?error=oauth_failed` renders an
  `alert alert-error`)
- [ ] 5.3 Help link ("Contact your administrator")
- [ ] 5.4 Small logo / mark (SVG in the brand bar)

**Tests to add:**
- `TestLogin_ProviderHasLogo`
- `TestLogin_HasErrorAlertTarget`
- `TestLogin_HasHelpLink`
- `TestLogin_HasLogoMark`

### Phase 6 — Cross-cutting polish (depends on Phases 1–5)

**Files:** `static/chat.js`, `static/chat.css`,
`daisyui.min.css`, plus per-page where utilities are applied

**Sub-tasks:**
- [ ] 6.1 Better empty state copy (a tone pass)
- [ ] 6.2 Improved focus states (remove any `focus:outline-none`)
- [ ] 6.3 Tooltip on icon-only buttons (`data-tooltip`)
- [ ] 6.4 Loading skeletons (wired up to actual loading
  states)

**Tests to add:**
- `TestUx_FocusStateGloballyVisible`
- `TestUx_TooltipPresent`

## 5. Commit cadence

One phase = one branch = one PR. Don't combine phases.
Each commit within a phase should pass `go test ./...`,
`golangci-lint run`, and `make css-check`.

Conventional commit format:
```
feat(web): <what you did in this commit>
```

Body:
- Reference the SPEC section: `Phase 2.1 of plans/ux-overhaul.md`
- Mention the daisyUI components used
- Mention the tests added or updated
- Note any visual verification screenshots taken

## 6. Visual verification

Take before / after screenshots in
`.review-screenshots/ux-overhaul/<phase>/`. Both desktop
(1280x900) and mobile (390x844), light and dark.

The screenshot helpers:
- `/tmp/screenshot.mjs` (desktop, 1280x900)
- `/tmp/screenshot-mobile.mjs` (mobile, 390x844)

Both take 3 args: `url`, `out.png`, `light|dark`.

The server: build with `go build -o /tmp/ragabast-ux .`,
run with the same config pattern as the prior polish work
(`auth_token: "ux-test-token"`, 2 OAuth providers, no
Ollama).

For pages that need populated data (e.g., documents table
with rows), ingest via the API with the bearer token.

## 7. Acceptance

When all phases are done, every box in the SPEC's
"Acceptance criteria (whole plan)" must be checked. The
new instance's final commit should reference the criteria
it satisfies.

## 8. Common pitfalls

- **Forgetting `make css` after adding new utility classes.**
  The daisyUI bundle is committed; without a rebuild, new
  classes don't make it into the shipped CSS.
- **Overriding daisyUI's focus styles with `focus:outline-none`.**
  It's an a11y antipattern. Use `focus-visible:` for custom
  styles.
- **Adding new pages or features.** This plan is scoped to
  the existing surface. New functionality is a separate
  project.
- **Combining phases.** Each phase is a PR. Reviewers sign
  off on Phase 1 first; Phase 2 can be reviewed in parallel
  with Phase 3.
- **Skipping the visual verification.** The test contracts
  pin the structure; the screenshots pin the look. Both are
  needed.
