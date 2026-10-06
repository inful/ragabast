# Makefile — CSS build for the embedded daisyui.min.css.
#
# Phase 0 of the Bulma -> DaisyUI migration. The compiled
# CSS at internal/web/static/daisyui.min.css is committed
# and shipped via go:embed; these targets exist so
# contributors can rebuild it after a template change and
# so CI can verify the committed artifact is current.
#
# Required tooling: Node 20+ (see .nvmrc) and npm. The
# release pipeline (goreleaser, distroless Docker) does
# NOT need Node; the committed CSS is what ships.

.PHONY: css css-check screenshot screenshot-before screenshot-diff screenshot-baseline

# css: rebuild internal/web/static/daisyui.min.css from
# static/src/daisyui.css. Run this after editing a
# template, the fallback renderers, or the daisyUI config
# in static/src/daisyui.css. Commit the regenerated file
# alongside your template change.
css:
	@command -v node >/dev/null 2>&1 || { echo "Node.js 20+ is required. See .nvmrc."; exit 1; }
	@command -v npm >/dev/null 2>&1 || { echo "npm is required."; exit 1; }
	npm ci
	npx @tailwindcss/cli \
		-i ./static/src/daisyui.css \
		-o ./internal/web/static/daisyui.min.css \
		--minify

# css-check: rebuild the CSS in a temporary copy and
# diff against the committed file. Exits non-zero if
# they differ, which is the signal that a contributor
# changed a template (or the daisyUI config) without
# running `make css`. The CI workflow's css-check job
# runs this on every push and pull request.
css-check:
	@command -v node >/dev/null 2>&1 || { echo "Node.js 20+ is required. See .nvmrc."; exit 1; }
	@command -v npm >/dev/null 2>&1 || { echo "npm is required."; exit 1; }
	npm ci
	@cp internal/web/static/daisyui.min.css /tmp/ragabast-daisyui.committed
	@$(MAKE) css >/dev/null
	@if diff -q /tmp/ragabast-daisyui.committed internal/web/static/daisyui.min.css >/dev/null 2>&1; then \
		echo "daisyui.min.css is up to date."; \
	else \
		echo "daisyui.min.css is STALE. Run 'make css' and re-commit."; \
		diff /tmp/ragabast-daisyui.committed internal/web/static/daisyui.min.css | head -40; \
		exit 1; \
	fi

# screenshot: capture screenshots of the local dev server at
# the three standard viewports (1920x1080 desktop, 1280x800
# laptop, 768x1024 tablet). Output goes to screenshots/actual/.
# The dev server must already be running on localhost:8080
# (or whatever CHAT_URL is set to below).
#
# Use as part of the before/after verification workflow:
#
#   1. (server up) make screenshot    # captures the current state
#   2. cp -r screenshots/actual screenshots/before
#   3. (make your CSS/HTML/template change)
#   4. make css && make screenshot    # rebuilds CSS, re-captures
#   5. make screenshot-diff           # before vs after
#
# Step 2 is just a rename — `make screenshot-before` is a
# convenience alias.
CHAT_URL ?= http://127.0.0.1:8080/
screenshot:
	@command -v node >/dev/null 2>&1 || { echo "Node.js 20+ is required. See .nvmrc."; exit 1; }
	@mkdir -p screenshots/actual
	./scripts/screenshot.sh "$(CHAT_URL)" screenshots/actual

# screenshot-before: rename the current screenshots/actual to
# screenshots/before so it survives the next `make screenshot`
# run. Used in the before/after workflow (see `screenshot`).
screenshot-before:
	@rm -rf screenshots/before
	@cp -r screenshots/actual screenshots/before
	@echo "Captured current state as screenshots/before/"

# screenshot-diff: diff screenshots/before (the "before" state
# captured via `make screenshot-before`) against screenshots/actual
# (the current state). Writes per-viewport pixel diffs and a
# side-by-side composite into screenshots/diff/. Exits non-zero
# if any viewport differs — useful as a CI gate.
screenshot-diff:
	@command -v node >/dev/null 2>&1 || { echo "Node.js 20+ is required. See .nvmrc."; exit 1; }
	@./scripts/screenshot-diff.sh screenshots/baseline screenshots/actual screenshots/diff

# screenshot-baseline: promote screenshots/actual/ to the
# "known good" baseline. Run this after a visual change has been
# reviewed and approved. CI's screenshot-diff job (if added) will
# alert when future PRs diverge from this baseline.
screenshot-baseline:
	@rm -rf screenshots/baseline
	@cp -r screenshots/actual screenshots/baseline
	@echo "Promoted screenshots/actual -> screenshots/baseline/"
	@git add screenshots/baseline/ && git status --short screenshots/baseline/

