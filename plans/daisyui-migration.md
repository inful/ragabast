# Bulma → DaisyUI migration

> Status: **planned**. Single-binary goal is preserved; CSS build is
> pre-compiled and committed, Node is required only locally + in CI
> (for the freshness check). goreleaser and the distroless Docker
> image are unchanged.

## Background

Bulma v1.0.4 has been the embedded CSS framework since the v1
migration. Today:

- `internal/web/static/bulma.min.css` (678 KB) is committed,
  embedded via `go:embed`, and served from `/static/bulma.min.css`.
- The CSP is `default-src 'self'; script-src 'self'; style-src 'self'`
  — strictly self-hosted, no CDN.
- Four HTML templates + three Go-string fallback renderers carry
  Bulma classes; two custom CSS files (`chat.css`, `login.css`) live
  alongside it.

**Why migrate now**: Bulma is in maintenance mode. The last
release was v1.0.4 in April 2025 (17 months ago at the time of
this plan); the maintainer ships occasional bug fixes but no new
components or features. DaisyUI ships releases every 1-2 days,
has an active roadmap, and exposes a larger component catalogue
(Bulma ~40 components vs DaisyUI 68, including `kbd`, `steps`,
`timeline`, `swap`, `drawer`, `toast`, etc.).

The current project state, with ~40 distinct Bulma classes in use
across 4 templates and 2 custom CSS files, is small enough that a
clean migration is feasible in 4 reviewable PRs.

## Goal

Replace Bulma with DaisyUI (built on Tailwind CSS 4) so the
web UI is on a maintained, actively-developed framework,
without changing the binary's "single embedded asset" model,
the strict self-hosted CSP, or the distroless Docker image.

After migration:

- The binary embeds `daisyui.min.css` (target: 100-200 KB after
  tree-shaking) instead of `bulma.min.css` (678 KB).
- Every page renders identically in light and dark modes (the
  DaisyUI default `light --default, dark --prefersdark`
  configuration mirrors today's Bulma behavior).
- CI enforces CSS freshness via a `make css --check` step that
  fails if a contributor changed a template without rebuilding
  the CSS.
- The custom CSS files (`chat.css`, `login.css`) keep their
  custom rules but reference DaisyUI CSS variables instead of
  Bulma ones.

## Non-goals

- **No theme toggle UI.** The single binary ships with
  `light --default, dark --prefersdark` only. OS-driven dark mode
  is the only dark-mode story. A user-facing theme picker is a
  follow-up.
- **No utility-class proliferation.** The migration uses
  DaisyUI's component classes (`.btn`, `.card`, `.navbar`) where
  they exist, and falls back to Tailwind utilities only for
  layout (grid, flex) and spacing — the same scope Bulma
  utilities cover today (`mt-2`, `mb-3`, `ml-2`, `mx-auto`,
  `text-*`, `font-*`). No drive-by adoption of utility classes
  beyond the minimum needed.
- **No `package.json` in goreleaser or Docker.** The release
  pipeline (`release.yml` and `Dockerfile`) is unchanged. Node is
  required locally and in CI; it is not required to build a
  release artifact.
- **No fork of Bulma or DaisyUI.** Both are MIT-licensed; the
  project pulls them via npm at build time and ships the
  compiled output under the same MIT terms. `THIRD_PARTY_LICENSES.md`
  is updated to add Tailwind CSS and DaisyUI entries.
- **No visual redesign.** The migration is a 1:1 class swap;
  spacing, color, and component choices match today. Any visual
  redesign is a follow-up.

## Architecture decision: pre-compiled CSS, committed, CI-checked

Three options were considered. The chosen approach is the
first; the others are recorded for the record.

### Chosen: pre-compile + commit + CI check

```
developer laptop:  make css  →  npm run build → daisyui.min.css
                                   ↓
                            git commit (committed artifact)
                                   ↓
GitHub CI:         make css --check  →  rebuild → diff against committed
                                   ↓
                            fail if drifted, pass if fresh
                                   ↓
goreleaser:        go build (uses committed CSS via go:embed)
                                   ↓
Docker:            gcr.io/distroless/static-debian12:nonroot
```

- **Pros**: distroless Docker stays; goreleaser pipeline is
  unchanged; binary still ships a single self-contained
  `daisyui.min.css`; CI is the freshness guarantee.
- **Cons**: contributors must install Node 20+ and run `make
  css` before committing template changes; the committed CSS
  occasionally drifts if a contributor forgets.

### Rejected: build CSS in goreleaser pre-hook

- **Pros**: CSS is never stale; contributor can forget the
  `make css` step.
- **Cons**: `release.yml` needs Node 20+ installed; goreleaser
  pre-build hook must run `npm ci`; CI runner image must include
  Node; the build is no longer "go build and ship" — it's a
  multi-language pipeline.

### Rejected: multi-stage Docker build with Node + Go

- **Pros**: no Node on the developer laptop; the Docker image
  is the single source of truth.
- **Cons**: Docker build context grows by ~500 MB (Node base
  image + npm cache); Dockerfile becomes multi-stage and
  complex; local development without Docker still needs Node;
  the distroless final image stays but the build is no longer
  one-liner.

## Scope (broken into 4 phases)

Each phase is a separate PR. Phases 0-1 ship without any
template change — they are tooling and a no-op CSS swap. Phases
2-3 are the actual rewrite, broken into reviewable chunks.

### Phase 0 — Add the build toolchain (no template changes)

Files added:

- `package.json` — npm manifest pinning `tailwindcss`,
  `@tailwindcss/cli`, and `daisyui` to current versions.
- `package-lock.json` — committed for reproducible installs.
- `static/src/daisyui.css` — Tailwind entry point:
  ```css
  @import "tailwindcss";
  @source "../../internal/web/templates/*.html";
  @source "../../internal/web/fallback_renderers.go";
  @plugin "daisyui" {
    themes: light --default, dark --prefersdark;
    include: button, input, select, textarea, checkbox, label,
             navbar, card, alert, badge, table, fieldset, divider,
             loading, join, indicator, status;
    logs: false;
  }
  ```
  The `include:` list restricts daisyUI to the components we
  actually use (plus a few for likely future use). The
  `@source` list tells Tailwind 4 which files to scan for
  utility-class usage.
- `Makefile` (new, repo root):
  ```make
  .PHONY: css css-check

  css:
  	npx @tailwindcss/cli \
  		-i static/src/daisyui.css \
  		-o internal/web/static/daisyui.min.css \
  		--minify

  css-check:
  	@cp internal/web/static/daisyui.min.css /tmp/daisyui.committed
  	@$(MAKE) css >/dev/null
  	@diff -q /tmp/daisyui.committed internal/web/static/daisyui.min.css \
  		|| (echo "daisyui.min.css is stale. Run 'make css' and re-commit."; exit 1)
  ```
- `.nvmrc` — pin Node 20 (matches GitHub Actions `ubuntu-latest`
  default).

Files modified:

- `AGENTS.md` — add a "CSS build" section under Common Commands
  documenting `make css` and `make css-check`. Note the Node
  20+ requirement. Add a TDD-style reminder: if a contributor
  changes a template, they must run `make css` before
  committing.
- `README.md` — add `make css` to the "Build and Test"
  section.
- `.github/workflows/ci.yml` — add a `css-check` job that runs
  on every push and PR. The job installs Node 20, runs
  `npm ci`, runs `make css-check`, and fails the build if the
  committed CSS is stale. The job is independent of the
  existing `test` and `lint` jobs so a Node failure does not
  mask a Go failure (or vice versa).

**No changes to**: `internal/web/static/bulma.min.css`,
`internal/web/static/static.go`, any template, any Go file,
`Dockerfile`, `.goreleaser.yml`, the release workflow, the
embedded templates, or the static handler.

Acceptance for phase 0:
- `make css` produces `internal/web/static/daisyui.min.css`
  (this file is committed but not yet referenced anywhere).
- `make css-check` passes against a fresh build.
- `go test ./...` is unchanged.
- `golangci-lint run` is unchanged.
- CI's new `css-check` job is green.

### Phase 1 — Ship both Bulma and DaisyUI side-by-side (no template changes)

Files modified:

- `internal/web/static/static.go` — add `daisyui.min.css` to
  the `staticAssets` allow-list. Bulma stays.
- `internal/web/static/static_test.go` — update
  `TestStaticAssets_AllowListIsExact` to include
  `daisyui.min.css` alongside the existing five entries.
- `internal/web/static/asset_version.go` — no change (the
  version map is built from `staticAssets` automatically).

The static handler now serves both files. Templates still
reference Bulma. This phase exists to confirm the build
artifact is sound, the file size is reasonable, and the static
handler serves the new file correctly. It's a 1-PR smoke test
that ships nothing to the user.

Acceptance for phase 1:
- `GET /static/daisyui.min.css` returns 200 with `Content-Type:
  text/css`.
- The fingerprint of the file (DaisyUI's
  `daisyui v5.x` version stamp or a `daisyui.min.css` selector
  check) appears in the body.
- The file is 100-200 KB (versus 678 KB Bulma).
- All existing tests pass.

### Phase 2 — Template rewrite (7 sub-phases, each a commit)

The class-name remap table. The left column is the Bulma
class as it appears in the codebase today; the right column
is the DaisyUI / Tailwind equivalent. Every mapping below is
applied in the order listed; the sub-phase a class appears
in is noted in the table.

| Bulma class | DaisyUI / Tailwind | Sub-phase |
|---|---|---|
| `container mt-4` | `container mx-auto mt-4` | 2f (chat fallback) |
| `title` | `text-2xl font-semibold` | 2a, 2c, 2d, 2e, 2f |
| `subtitle` | `text-base text-base-content/70` | 2a, 2c, 2d, 2e, 2f |
| `title is-5` | `text-xl font-semibold` | 2d |
| `subtitle is-6` | `text-sm text-base-content/70` | 2d |
| `field` | `fieldset` (wrap group) or remove (single input) | 2a, 2c, 2f |
| `control` | remove (DaisyUI has no equivalent wrapper) | 2a, 2c, 2f |
| `label` | DaisyUI `<label class="label">` (component, not a string of utilities) | 2a, 2c, 2f |
| `input` | `input` (unchanged) | 2a, 2c, 2f |
| `textarea` | `textarea` (unchanged) | 2f |
| `select is-fullwidth` + wrapper | `select w-full` (no wrapper) | 2c |
| `checkbox` + `<label>` | `checkbox` + label (DaisyUI uses native `<input type="checkbox" class="checkbox">`) | 2c, 2f |
| `button is-primary` | `btn btn-primary` | 2f |
| `button is-info` | `btn btn-info` | 2c |
| `button is-small` | `btn btn-sm` | 2c, 2d, 2e, 2f |
| `button is-danger` | `btn btn-error` | 2e |
| `button` (no modifier) | `btn` | 2c, 2d, 2e, 2f |
| `notification is-warning` | `alert alert-warning` | 2c, 2d |
| `tag is-info` | `badge badge-info` | 2d, 2e |
| `tags mb-3` (container) | `flex flex-wrap gap-2 mb-3` | 2d |
| `box` | `card` | 2c, 2d, 2e, 2f |
| `content` | `prose` (DaisyUI 5 has a `prose` for the typography rules Bulma's `.content` provides) | 2d, 2f |
| `content chat-turn` | `prose chat-turn` | 2g |
| `columns is-multiline` | `grid grid-cols-1 gap-4` (responsive: `md:grid-cols-2` where today is side-by-side) | 2d |
| `column is-full` | full grid row (handled by grid layout, not class) | 2d |
| `columns` (with two children) | `grid grid-cols-1 md:grid-cols-2 gap-4` | 2c |
| `column` | grid child (no class) | 2c |
| `level` + `level-left/right/item` | `flex items-center justify-between` | 2d |
| `level-item` | `flex` | 2d |
| `level mb-4` | `flex items-center justify-between mb-4` | 2d |
| `navbar` + `navbar-brand/start/end/item/menu` | `navbar` + `navbar-start/end` (DaisyUI 5 drops `navbar-menu` and `navbar-brand`; the same effect is achieved by a `<div class="navbar">` with a child `<div class="navbar-start">` and a sibling `<div class="navbar-end">`) | 2f |
| `is-active` (on `navbar-menu`) | remove (DaisyUI 5 has no `is-active`; the menu is always rendered) | 2f |
| `table is-fullwidth is-striped` | `table table-zebra w-full` | 2e |
| `is-striped` | `table-zebra` | 2e |
| `is-fullwidth` | `w-full` | 2e |
| `is-multiline` | (handled by grid + flex-wrap, not a class) | 2d |
| `is-size-7` | `text-xs` | 2d, 2e |
| `has-text-grey` | `text-base-content/60` | 2d, 2e, 2f |
| `has-text-weight-semibold` | `font-semibold` | 2d, 2e, 2f |
| `help` | `text-xs text-base-content/60` (no `.help` class in DaisyUI; use a `<p>` with utility classes) | 2f |
| `mt-2` / `mt-4` / `mb-3` / `mb-4` / `ml-2` | unchanged (Tailwind utilities) | throughout |
| `is-light` / `has-background-light` | remove entirely (these are dark-mode bugs in Bulma; DaisyUI handles this automatically) | 2f |
| `source-kind-option` (custom class) | unchanged (custom CSS, defined in `chat.css`) | 2c, 2f |
| `source-kind-emoji` / `source-kind-name` (custom) | unchanged | 2c, 2f |
| `chat-turn` (custom) | unchanged | 2g |
| `chat-msg` (custom) | unchanged | 2g |
| `chat-turn-divider` (custom) | unchanged (still uses `border-top: 1px solid var(--color-base-300)` after phase 2b) | 2b, 2g |
| `lead` (login page) | `text-sm text-base-content/60` | 2a |
| `meta` (login page) | `text-xs text-base-content/50 text-center` | 2a |
| `providers` (login page) | `flex flex-col gap-2` | 2a |

The sub-phases. Each sub-phase is a single commit; the order
is chosen so each commit renders standalone and reviewable.

#### Sub-phase 2a — Login page (`templates/login.html`, `static/login.css`)

- No navbar dependency, no shared chrome.
- Easiest visual diff against the existing dark-themed login.
- Replaces the `lead`, `meta`, `providers` custom classes with
  DaisyUI/Tailwind utilities.
- Updates `login.css` comments (drops the "driven by Bulma
  variables" wording; the file no longer references Bulma).

#### Sub-phase 2b — Custom CSS file updates (`chat.css`, `login.css`)

- `chat.css`: `var(--bulma-border-weak)` →
  `var(--color-base-300)`. (DaisyUI's equivalent of Bulma's
  weak-border token.)
- `chat.css`: comment block at the top — drop the "Bulma's
  default box" wording.
- `login.css`: comment block — drop the "Bulma variables"
  reference.
- No new rules; this is a search-and-replace commit.

#### Sub-phase 2c — Search page (`templates/search.html`)

- Two-column layout: tag + category side-by-side. The
  `columns` → `grid grid-cols-1 md:grid-cols-2` swap.
- `select is-fullwidth` → `select w-full` (with the
  `<div class="select">` wrapper removed).
- `box` → `card` for the query field (it doesn't use `box` —
  but the form section does).
- `button is-info` → `btn btn-info` for the Search button.
- `help` paragraph — utility classes.

#### Sub-phase 2d — Search results fragment (`templates/search_results.html`)

- `columns is-multiline` → `grid grid-cols-1 gap-4` (single
  full-width column in the current design; the
  `is-multiline` was carried over but unused at the current
  result count).
- `level` (query + new search button) → `flex justify-between`.
- `box` → `card` for each result card.
- `tag is-info` (filter chips) → `badge badge-info`.
- `notification is-warning` (no results) → `alert
  alert-warning`.
- `title is-5` / `subtitle is-6` → text utilities.
- `is-size-7` / `has-text-grey` / `has-text-weight-semibold`
  → Tailwind utilities.

#### Sub-phase 2e — Documents page (`fallback_renderers.go`, documents body)

- `table is-fullwidth is-striped` → `table table-zebra w-full`.
- `tag is-info` (tag chips per row) → `badge badge-info`.
- `button is-small` (Previous/Next/Delete) → `btn btn-sm`.
- `button is-danger` (Delete) → `btn btn-error`.
- `notification` (empty state) → `alert alert-info`.
- `has-text-grey` / `has-text-weight-semibold` → utility classes.
- `is-size-7` → `text-xs`.

#### Sub-phase 2f — Chat landing (`fallback_renderers.go`, chat body + header partial)

This is the largest sub-phase. Every other page depends on the
navbar (defined in the header partial at the top of
`fallback_renderers.go`); doing it last means the navbar is
defined against the full set of pages that use it.

- `navbar` + `navbar-menu` + `navbar-brand` + `navbar-start` +
  `navbar-end` + `navbar-item` + `is-active` → DaisyUI's
  `navbar` + `navbar-start` + `navbar-end` pattern (no
  `navbar-menu`, no `is-active`).
- The `field` + `control` + `label` wrapper soup collapses
  around the message textarea. The Sources checkbox group
  keeps a `<fieldset>` wrapper.
- `button is-primary` (Send) → `btn btn-primary`.
- `button is-small` (Sign out) → `btn btn-sm`.
- `tag` (Thinking indicator) → `badge`.
- `help` (keyboard shortcut hint) → utility classes.
- `is-light` and `has-background-light` removals (DaisyUI
  handles dark mode automatically; no special modifier
  needed).
- The container `container mt-4` → `container mx-auto mt-4`.

#### Sub-phase 2g — Chat message fragment (`templates/chat_message.html`)

- `box` (user/assistant message boxes) → `card`.
- `content chat-turn` → `prose chat-turn`.
- `has-text-weight-semibold` → `font-semibold`.
- `<hr class="chat-turn-divider">` — unchanged; the custom
  CSS in `chat.css` already references the new variable after
  sub-phase 2b.

#### Phase 2 acceptance

After each sub-phase, the following must hold:

- `go test ./...` is green.
- `golangci-lint run` is clean.
- The new `css-check` CI job is green (sub-phase 2c onwards —
  Bulma class changes that don't affect DaisyUI output may not
  trigger a rebuild, which is fine; the check catches
  **stale** CSS, not **dirty** templates).
- A visual screenshot of the rewritten page is attached to
  the PR. For the chat landing, the screenshot must show the
  navbar, the chat form, the empty state, and a sample
  assistant reply (from a test fixture).

### Phase 3 — Drop Bulma, update tests, update docs

Files modified:

- `internal/web/static/static.go` — remove `bulma.min.css`
  from the `staticAssets` allow-list.
- `internal/web/static/static_test.go`:
  - `TestStaticHandler_ServesBulmaCSS` — delete.
  - `TestStaticHandler_StaticAssetsInBinaryVerify` — update
    the selector fingerprint from `.button` / `.input` /
    `.navbar` (Bulma selectors) to `btn` / `input` / `navbar`
    (DaisyUI selectors). The DaisyUI selectors are emitted
    with a different transform — pin only on selectors that
    survive minification, e.g. `.btn,` and `.input,` and
    `.navbar` literal.
  - `TestStaticHandler_TemplatesReferenceBundledAssets` —
    the `/static/bulma.min.css` URL string changes to
    `/static/daisyui.min.css`.
  - `TestStaticAssets_AllowListIsExact` — drop the
    `bulma.min.css` entry.
- `internal/web/static/asset_version_test.go`:
  - `TestAssetURL_KnownAndUnknownNames` — the literal
    `"bulma.min.css"` becomes `"daisyui.min.css"`.
- `internal/web/chat_landing_test.go`:
  - `TestChatLanding_SendButtonUsesPrimaryAction` — the
    `button is-primary` literal becomes `btn btn-primary`;
    the failure message text updates to match.
- `internal/web/documents_chips_test.go`:
  - `TestDocumentsFallback_RendersTagsAsChips` — the
    `<span class="tag is-info">alpha</span>` literal becomes
    `<span class="badge badge-info">alpha</span>`. The
    comment block explaining the chip pattern rewrites.
- `internal/web/chat_input_shortcut_test.go`:
  - `TestChatLanding_PlaceholderHintsKeyboardShortcut` — the
    regex pins a Bulma `.help` paragraph; update to pin a
    `<p>` with the right Tailwind utility classes.
- `internal/web/dark_mode_adaptive_test.go`:
  - `TestDarkMode_NoLightModifiersOnAdaptiveSurfaces` — the
    `is-light` / `has-background-light` assertions stay (they
    pin the *absence* of those classes, which still holds
    post-migration; DaisyUI doesn't have them, so the test
    trivially passes — keep it as a regression guard).
  - `TestDarkMode_TurnDividerUsesAdaptiveVariable` — the
    `var(--bulma-border-weak)` regex updates to
    `var(--color-base-300)`.
- `internal/web/page_chrome_nav_test.go`:
  - `TestSearchTemplate_NoRedundantNav` — comment update
    only; the test is class-agnostic.
- `internal/web/security_headers.go` and
  `internal/web/security_headers_test.go`:
  - Comments referencing Bulma updated to reference DaisyUI.
  - The `cdn.jsdelivr.net` assertion in
    `TestSecurityHeadersMiddleware` stays — it pins the
    *absence* of that origin, which holds for DaisyUI too.
- `THIRD_PARTY_LICENSES.md`:
  - Remove the `## Bulma v1.0.4 — MIT` section.
  - Add:
    ```
    ## Tailwind CSS — MIT
    Source: https://github.com/tailwindlabs/tailwindcss
    License: https://github.com/tailwindlabs/tailwindcss/blob/master/LICENSE

    ## daisyUI v5.x — MIT
    Source: https://github.com/saadeghi/daisyui
    License: https://github.com/saadeghi/daisyui/blob/master/LICENSE
    ```
- `Dockerfile`:
  - Update the comment block "Bulma (MIT) and htmx
    (BSD-2-Clause) are embedded…" to "DaisyUI, Tailwind CSS
    (both MIT) and htmx (BSD-2-Clause) are embedded…".
  - The `COPY THIRD_PARTY_LICENSES.md` line stays (the file
    is updated, not removed).
- `.goreleaser.yml`:
  - Update the matching comment block in the `files:`
    section.
- `SECURITY.md`:
  - Update the section that mentions "Bulma and htmx" to
    "DaisyUI, Tailwind CSS, and htmx".
- `plans/architecture.md`:
  - Update the "HTMX/Bulma web UI" line to "HTMX/DaisyUI
    web UI" (line 4 of the existing file).
- `AGENTS.md`:
  - Add the CSS build section (from phase 0) — already
    shipped.
  - Update the "Web Templates" section's note about
    responsive design to mention that Tailwind utilities are
    available (no behavior change, just documentation).
- `.gitignore`:
  - `node_modules/` is added (npm install target).
  - `internal/web/static/daisyui.min.css` is **not** in
    `.gitignore` — it is the committed artifact.
- `static/src/` and `Makefile` (already added in phase 0) —
  no further changes.

Files deleted:

- `internal/web/static/bulma.min.css` (678 KB).

Acceptance for phase 3:

- `go test ./...` is green.
- `golangci-lint run` is clean.
- `make css-check` is green.
- `THIRD_PARTY_LICENSES.md` matches the embedded assets
  (no Bulma entry; Tailwind + DaisyUI entries present).
- `grep -r "bulma" --include="*.go" --include="*.html"
  --include="*.md" --include="*.yml" .` returns no hits
  outside of `THIRD_PARTY_LICENSES.md` (where the historical
  "this used to be Bulma" note is allowed) and `.planning/`
  (where this SPEC lives).
- Visual regression check: every page renders identically in
  light mode and identically in dark mode (compared to the
  pre-migration screenshots in `.review-screenshots/`).
- Bundle size: the binary's `internal/web/static/` directory
  is smaller than before the migration (Bulma 678 KB +
  custom CSS 6 KB + htmx 47 KB → DaisyUI ~150 KB + custom
  CSS 6 KB + htmx 47 KB).

## Visual verification checklist

Captured before phase 0 and compared at every sub-phase of
phase 2. Screenshots live in `.review-screenshots/daisyui-migration/`.

| Page | Light | Dark | Notes |
|---|---|---|---|
| `/auth/login` (when ≥ 2 providers configured) | ✓ | ✓ | No navbar; dark page. |
| `/` (chat landing, empty) | ✓ | ✓ | Navbar, title, form, thinking indicator, jump-to-latest button (hidden by default). |
| `/` (chat landing, post-submit) | ✓ | ✓ | Submit a query with a fake service; verify the loading state, the response fragment, and the jump-to-latest button visibility toggle. |
| `/search` (empty) | ✓ | ✓ | Two-column form (tag + category), single-column fields. |
| `/search` (results, ≥ 1) | ✓ | ✓ | Filter chips as `badge`, results as `card`, "no results" alert when empty. |
| `/documents` (empty) | ✓ | ✓ | Empty-state alert. |
| `/documents` (populated) | ✓ | ✓ | Striped table, tag chips as `badge`, delete form with confirm checkbox. |
| OS-driven dark mode flip (prefers-color-scheme: dark) | n/a | ✓ | Toggle OS dark mode; every page flips without reload. |

## Risks

- **Contributors forget `make css`.** The CI `css-check` job
  catches it, but a contributor with CI disabled (or a
  forked-repo PR with no secrets) could land a stale CSS. The
  pre-PR local-check command in `AGENTS.md` is the secondary
  guard: `make css && go test ./...`.
- **DaisyUI 5 changes its class names in a future major.**
  This is a normal library-evolution risk; we pin via
  `package-lock.json` and bump deliberately. The
  `include:` list in `static/src/daisyui.css` keeps the
  blast radius small — we only ship the components we use.
- **Visual regression in dark mode.** The Bulma 1.0.4 dark
  mode uses `prefers-color-scheme` media queries inside
  `bulma.min.css`; the DaisyUI equivalent is the
  `dark --prefersdark` theme. The `dark_mode_adaptive_test.go`
  test pins the relevant dark-mode behavior (no
  `is-light` / `has-background-light` classes, the
  `--color-base-300` divider color). If dark mode regresses,
  that test fails.
- **Form field wrapper collapse is structurally invasive.**
  Bulma uses `<div class="field"><div class="control"><input
  class="input"/></div></div>` per field. DaisyUI drops the
  wrapper soup. The structural change is a bigger diff than
  the class name swap; sub-phases 2a / 2c / 2f are the
  careful ones.
- **`columns` → `grid` swap breaks side-by-side layouts.**
  Bulma's `.columns` is auto-flexing. The DaisyUI equivalent
  is explicit: `grid grid-cols-1 md:grid-cols-2 gap-4`. The
  `md:` breakpoint mirrors Bulma's desktop behavior; mobile
  collapses to a single column. Visual verification in
  `.review-screenshots/daisyui-migration/` is the safety
  net.
- **DaisyUI doesn't have a `help` class.** The
  `chat_input_shortcut_test.go` regex has to be rewritten.
  Documented in the test update list.
- **DaisyUI uses `card` where Bulma uses `box`.** The
  semantic name is the same; the visual treatment is
  slightly different (DaisyUI's `card` has a header/body
  split; Bulma's `box` is a flat panel). Where we used
  `.box` as a flat surface (most of the result cards, the
  chat message box), `<div class="card">` (no body) renders
  flat. Where we used `.box` with structured content, we may
  need `<div class="card"><div class="card-body">…</div></div>`.
  Visual diffs in phase 2d (search results) and 2g
  (chat message) are the safety net.
- **Huma / Stoplight Elements dependency in the docs
  page.** The `unpkg.com` carve-out in the CSP for `/docs`
  is unaffected by this migration; no change needed.
- **Bulma 1.0.4 security CVE lands after we drop it.** With
  the migration complete, we're not exposed; the binary
  embeds DaisyUI, not Bulma. The CI `govulncheck` job keeps
  scanning Go dependencies; we add a Dependabot-equivalent
  for `package.json` (via GitHub's native `npm` support in
  Dependabot) as a follow-up if it isn't already configured.

## Tests (TDD-shaped)

For each sub-phase, the test comes first or the test rewrite
ships with the change. The new tests added by this plan:

- `internal/web/static/static_test.go`:
  - `TestStaticHandler_ServesDaisyUICSS` (mirrors the
    `TestStaticHandler_ServesBulmaCSS` it replaces).
  - Update `TestStaticHandler_StaticAssetsInBinaryVerify`
    to assert DaisyUI selectors (`.btn,` / `.input,` /
    `.navbar`).
- `internal/web/static/static_test.go`:
  - `TestStaticHandler_CSSBundleIsUnder250KB` — guards
    against a Tailwind config regression that re-includes
    all utilities and blows the bundle past Bulma's size.
- `internal/web/dark_mode_adaptive_test.go`:
  - `TestDarkMode_ColorBase300UsedByDivider` — the
    `var(--color-base-300)` assertion (currently
    `var(--bulma-border-weak)`).
- `internal/web/chat_landing_test.go`:
  - Update `TestChatLanding_SendButtonUsesPrimaryAction` to
    pin `btn btn-primary` instead of `button is-primary`.

For the CI check, the `css-check` job is the new test
itself: any PR with a stale `daisyui.min.css` fails the
build.

For visual verification, the screenshots in
`.review-screenshots/daisyui-migration/` are the manual
checks; the visual review is the project owner eyeballing
each page.

## Out of scope (deferred)

- **Theme picker / multiple themes.** The shipped theme is
  `light --default, dark --prefersdark`. Adding a navbar
  theme-toggle is a follow-up; it requires a small inline
  script (CSS variable swap on click) and is incompatible
  with the strict CSP without a new `/static/theme.js` asset.
- **Component additions.** DaisyUI 5 ships components
  Bulma doesn't have (`kbd`, `steps`, `timeline`, `toast`,
  `drawer`, `modal`, `accordion`, `dropdown`, `swap`, `tab`,
  `pagination`, `stat`, etc.). None of these are needed by
  the current UI; adding them is a separate project per
  use-case.
- **A "light" Tailwind utility adoption beyond the
  migration's needs.** The migration uses utilities only
  where the Bulma equivalents already use them (spacing,
  text color, font weight, grid). A wholesale "rewrite the
  page templates to use utilities everywhere" pass is out
  of scope and would obscure the diff.
- **DaisyUI's `themes: all` mode.** 35 built-in themes
  would bloat the CSS. The shipped configuration enables
  only `light` and `dark`. Adding more themes is a follow-up
  if the operator wants a theme picker.
- **Migration of the Huma `/docs` page.** The OpenAPI
  viewer at `/docs` is rendered by Huma and uses
  Stoplight Elements, which has its own styling. The CSP
  carve-out for `unpkg.com` stays. No changes to `/docs`.
- **Dependabot configuration for `package.json`.** The
  repo's GitHub-native Dependabot setup (if any) currently
  covers Go modules; npm support is a follow-up. Until
  then, contributors bump `package.json` manually.

## Acceptance criteria (whole plan)

The migration is complete when:

1. `grep -r "bulma" --include="*.go" --include="*.html"
   --include="*.md" --include="*.yml" .` returns no hits
   outside of `THIRD_PARTY_LICENSES.md` (historical note)
   and `.planning/` (this SPEC).
2. `internal/web/static/bulma.min.css` does not exist.
3. `internal/web/static/daisyui.min.css` exists, is
   committed, and is 100-200 KB.
4. `go test ./... -count=1 -race` is clean.
5. `golangci-lint run ./...` is clean.
6. `make css && make css-check` is clean.
7. CI's `css-check` job is green on `main` and on a test
   branch where the CSS is intentionally stale (manual
   verification — a contributor would do this once to
   confirm the check actually fires).
8. The visual verification checklist above is complete;
   every page renders identically in light and dark mode
   to the pre-migration screenshots.
9. `THIRD_PARTY_LICENSES.md` lists Tailwind CSS and DaisyUI
   with the correct license / source links; no Bulma entry.
10. `Dockerfile` and `.goreleaser.yml` are unchanged
    (their *comments* changed; the file content / COPY /
    files: lists are byte-identical to before phase 0).
11. The release binary on the distroless image still
    embeds the CSS via `go:embed`; the Dockerfile and
    goreleaser config did not need to add Node.js.
12. The CSP `default-src 'self'; script-src 'self';
    style-src 'self'` is unchanged.

## Commit sequence (final)

This is the order the PRs land on `main`. Each is its own
PR with screenshots.

1. **PR 1 (Phase 0)** — Toolchain: add `package.json`,
   `Makefile`, `static/src/daisyui.css`, `.nvmrc`,
   `node_modules/` via `.gitignore`. Update `AGENTS.md`,
   `README.md`, `.github/workflows/ci.yml`. Ship a
   pre-built `internal/web/static/daisyui.min.css` that's
   not yet referenced by any template.
2. **PR 2 (Phase 1)** — Add `daisyui.min.css` to the
   static allow-list. Ship both Bulma and DaisyUI; no
   template change.
3. **PR 3.1 (Phase 2a)** — Login page.
4. **PR 3.2 (Phase 2b)** — Custom CSS files
   (`chat.css`, `login.css`).
5. **PR 3.3 (Phase 2c)** — Search page.
6. **PR 3.4 (Phase 2d)** — Search results fragment.
7. **PR 3.5 (Phase 2e)** — Documents page.
8. **PR 3.6 (Phase 2f)** — Chat landing (largest).
9. **PR 3.7 (Phase 2g)** — Chat message fragment.
10. **PR 4 (Phase 3)** — Drop Bulma; update remaining
    tests; update docs and license; final visual review.

Total: 9 PRs. Each is reviewable in isolation. The riskiest
are 3.1 (form-field wrapper collapse), 3.3 (`columns` →
`grid`), 3.6 (the navbar is shared by every page), and
PR 4 (the test updates and the Bulma removal — these are
mechanical but the test fingerprint selectors need careful
choice).
