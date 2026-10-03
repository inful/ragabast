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

.PHONY: css css-check

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
