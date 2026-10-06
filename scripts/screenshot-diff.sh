#!/usr/bin/env bash
# scripts/screenshot-diff.sh — Thin shell wrapper around screenshot-diff.mjs.
# See screenshot.sh for the rationale (the .mjs file does the work,
# this script is a stable Make entry point).

set -euo pipefail

BEFORE="${1:?Usage: $0 <before-dir> <after-dir> <diff-dir>}"
AFTER="${2:?Usage: $0 <before-dir> <after-dir> <diff-dir>}"
DIFF_DIR="${3:?Usage: $0 <before-dir> <after-dir> <diff-dir>}"

if ! command -v node >/dev/null 2>&1; then
  echo "Node.js 20+ is required (see .nvmrc)." >&2
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec node "${SCRIPT_DIR}/screenshot-diff.mjs" "$BEFORE" "$AFTER" "$DIFF_DIR"
