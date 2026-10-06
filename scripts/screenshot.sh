#!/usr/bin/env bash
# scripts/screenshot.sh — Thin shell wrapper around screenshot.mjs.
#
# Why both: the .mjs file is what does the real work (puppeteer-core
# is JS), but the Makefile is more readable with a shell target. This
# wrapper exists so `make screenshot` can call a stable shell entry
# point even if the JS implementation moves between runtimes.

set -euo pipefail

URL="${1:?Usage: $0 <url> <output-dir>}"
OUT_DIR="${2:?Usage: $0 <url> <output-dir>}"

if ! command -v node >/dev/null 2>&1; then
  echo "Node.js 20+ is required (see .nvmrc)." >&2
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec node "${SCRIPT_DIR}/screenshot.mjs" "$URL" "$OUT_DIR"
