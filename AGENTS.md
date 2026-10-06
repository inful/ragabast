# AGENTS.md

This file provides guidance to agents when working with code in this repository.

## Project Overview

## Technology Stack

- **Language**: Go 1.25
- **API Framework**: HUMA v2 with go-chi/chi router
- **Web Framework**: HTMX (server-side rendering)
- **CLI Framework**: Kong
- **Linter**: golangci-lint version v2.8.0

## Non-Obvious Project-Specific Information

### Agent instructions

- When working with software libraries, API, third party tools, etc, first check with the context7 mcp for the most up to date documentations.
- For anything that involves complex analysis, planning and designing, use sequential thinking mcp.
- Always use conventional commits; amend if necessary to keep history clean.
- When adding a new feature, use a strict TDD approach.
- When fixing a bug, make sure you first create a test to reproduce the bug before proceeding to fix it.
- When testing the huma endpoints, use the humatest package.
- Stage only relevant files for each commit (avoid `git add -A`).
- Fix all `golangci-lint` issues before committing. Use the --fix flag to ensure minimal intervention is necessary.
- Run the full test suite (`go test ./...`) and ensure it passes before committing.

### CLI Framework
- Uses `github.com/alecthomas/kong` for CLI parsing
- Commands are defined in a struct hierarchy in `cmd/root.go`
- Parsed with `kong.Parse(&CLI)`

### API Framework
- Uses `github.com/danielgtaylor/huma/v2` with `go-chi/chi` adapter
- All API operations must be registered via `huma.Register()`
- OpenAPI documentation auto-generated at `/docs`

### Test Structure
- Tests are co-located with source files (e.g., `db_test.go` next to `db.go`)
- All tests use testify assertions
- Testifylint compliant
- **Use testing package helpers**: Always use `t.Setenv()` instead of `os.Setenv()` and `t.Chdir()` instead of `os.Chdir()` in tests
  - These helpers automatically clean up after tests
  - No need for manual defer cleanup functions
  - Prevents environment pollution between tests
  - Enforced by golangci-lint's `usetesting` rule

## Code Quality Standards

### Linter Configuration
- **Tool**: golangci-lint v2
- **Issues Allowed**: 0
- **Always run with**: `--fix` flag to auto-fix formatting

### Cyclomatic Complexity
- **Limit**: 10 per function
- **Enforcement**: golangci-lint with cyclop rule

### Comments
- **Style**: All comments must end with periods (godot rule)
- **Enforcement**: golangci-lint with godot rule

### Test Assertions
- **Library**: testify
- **Compliance**: testifylint compliant
- **Style**: Use `require.NoError(t, err)` for errors, `assert.Equal(t, expected, actual)` for values

### Testing Strategy
- **Integration Tests**: Must test complete user flows through web handlers
- **Key principle**: Tests should catch issues that users would encounter

### Web Templates
- **Requirements**: 
  - Responsive design
  - HTMX integration

### Code Organization
- **Tests**: Co-located with source files (e.g., `db_test.go` next to `db.go`)
- **Integration Tests**: Separate files with `_integration_test.go` or feature-specific naming
- **Package Structure**: Internal packages for domain logic

## Common Commands

### Build and Test
```bash
# Build
go build -o ragabast

# Run all tests
go test ./... -v -cover

# Run specific package tests
go test ./internal/something -v

# Race detector
go test -race ./...
```

### CSS build (Bulma → DaisyUI migration, Phase 0)
```bash
# Rebuild the embedded daisyui.min.css from
# static/src/daisyui.css. Required after editing a template,
# the fallback renderers in internal/web/, or the daisyUI
# config in static/src/daisyui.css. Commit the regenerated
# file alongside your template change.

make css

# Verify the committed daisyui.min.css is current.
# Exits non-zero if a contributor changed a template
# without re-running `make css`. CI runs this on every
# push and pull request (the `css-check` job in
# .github/workflows/ci.yml).

make css-check
```

> Requires Node 20+ (see `.nvmrc`) and npm. The release
> pipeline (goreleaser, distroless Docker) does NOT need
> Node — the committed CSS is what ships, embedded via
> `go:embed`. See `plans/daisyui-migration.md` for the
> full migration plan.

### Code Quality
```bash
# Lint check
golangci-lint run

# Auto-fix issues
golangci-lint run --fix

# Format code
golangci-lint run --fix
```

## Pre-Commit Checklist

Before committing changes, ensure:

1. **All tests pass**: `go test ./... -v`
2. **No linter issues**: `golangci-lint run --fix`
5. **Documentation updated**: Check for outdated references
6. **No broken links**: Verify all file references in docs

## Layout / CSS / template changes — visual verification

For any change that touches `chat.css`, `login.css`,
`internal/web/static/*.css`, the daisyUI config in
`static/src/daisyui.css`, the chat/search/documents
templates, or `internal/web/fallback_renderers.go`,
**always do a before/after screenshot diff** before
claiming the fix works. Tests passing is not the same as
the layout looking right — the v0.16.9 chat-layout fix
shipped with passing tests but the user still saw a
narrow chat, and the v0.16.10 fix was needed to
actually widen the bubbles.

The tooling is in `scripts/`:

```bash
# 1. Start the dev server (port 8080 by default).
RAGABAST_CONFIG=/path/to/config.yml ./ragabast serve &

# 2. Capture the current (broken) state as the baseline.
make screenshot
make screenshot-baseline       # promotes actual/ -> baseline/

# 3. Make the fix.

# 4. Rebuild CSS (if you changed templates or daisyUI
#    sources) and re-capture.
make css
make screenshot

# 5. Diff. Exit 0 = no pixel differences; exit 1 = some
#    differ. Per-viewport PNGs land in screenshots/diff/ —
#    open the *-side-by-side.png for a human review (BEFORE
#    / DIFF / AFTER stacked with labels).
make screenshot-diff
```

`make screenshot-diff` exits non-zero when the actuals
diverge from the baseline — wire it as a CI check if/when
the project's CI moves from "build only" to "build + visual
gate". `scripts/scripts_test.go` already pins the structure
(scripts exist, executable, Makefile registers the
targets, package.json has the deps) so a contributor who
breaks the tooling gets a red test rather than a silent
failure during a layout bug report.

The capture script uses `puppeteer-core` driving the
system Google Chrome (no extra browser download) at three
standard viewports: 1920×1080, 1280×800, 768×1024. Override
`CHAT_URL` in the Make target if the dev server isn't on
`localhost:8080`.

To capture a populated chat (for verifying bubble
layouts, which don't appear in the empty state), inject
sample HTML via the browser DevTools before running
`make screenshot` — the tool captures whatever is at the
URL when it runs.

## Common Pitfalls to Avoid

## Testing Guidelines

### Unit Tests
- Co-located with source files
- Use testify assertions
- Test all error paths
- Mock external dependencies

### Integration Tests
- Test API endpoints

### Test Data
- Clean up after tests
- Use realistic scenarios

## Deployment Considerations

## Future Enhancements

## References

- **Main Documentation**: `README.md`

<!-- gortex:communities:start -->
<!-- gortex:skills:start -->
## Community Skills

| Area | Description | Skill |
|------|-------------|-------|
| Service 9 Dirs | 469 symbols | `/gortex-service-9-dirs` |
| Web 5 Dirs | 276 symbols | `/gortex-web-5-dirs` |
| Vector 2 Dirs Background | 214 symbols | `/gortex-vector-2-dirs-background` |
| Vector 7 Dirs | 199 symbols | `/gortex-vector-7-dirs` |
| Models 3 Dirs | 164 symbols | `/gortex-models-3-dirs` |
| Service 4 Dirs | 157 symbols | `/gortex-service-4-dirs` |
| Service 7 Dirs | 152 symbols | `/gortex-service-7-dirs` |
| Config 4 Dirs | 147 symbols | `/gortex-config-4-dirs` |
| Web 6 Dirs Int | 124 symbols | `/gortex-web-6-dirs-int` |
| Chunker 5 Dirs | 123 symbols | `/gortex-chunker-5-dirs` |
| 2 Dirs Fprintf | 83 symbols | `/gortex-2-dirs-fprintf` |
| Web 6 Dirs Trimspace | 77 symbols | `/gortex-web-6-dirs-trimspace` |
| Config 2 Dirs | 67 symbols | `/gortex-config-2-dirs` |
| 1 Dirs Constructjsonfromtext | 52 symbols | `/gortex-1-dirs-constructjsonfromtext` |
| Vector Newopenaillmclientwithoptions | 48 symbols | `/gortex-vector-newopenaillmclientwithoptions` |
| Vector 2 Dirs Applyenvoverrides | 47 symbols | `/gortex-vector-2-dirs-applyenvoverrides` |
| Vector 2 Dirs Ingestdocument | 42 symbols | `/gortex-vector-2-dirs-ingestdocument` |
| 3 Dirs | 35 symbols | `/gortex-3-dirs` |
| Vector 3 Dirs | 33 symbols | `/gortex-vector-3-dirs` |
| 1 Dirs Handlechatmessage | 32 symbols | `/gortex-1-dirs-handlechatmessage` |
<!-- gortex:skills:end -->

<!-- gortex:communities:end -->
