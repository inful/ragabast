#!/usr/bin/env bash
#
# ingest-with-preflight.sh — small example showing how an external
# service can use ragabast's fingerprint preflight + ingest/file
# endpoints together to avoid sending unchanged documents.
#
# Usage:
#   RAGABAST_URL=https://ragabast.example.com \
#   AUTH_TOKEN=... \
#   ./ingest-with-preflight.sh <uid> <path/to/doc.md>
#
# Requires: bash, curl, sha256sum, jq.
#
# What it does:
#   1. Computes the SHA-256 fingerprint of the local file
#      (same algorithm the ragabast parser uses for the
#      auto-generated fingerprint; see internal/parser/docbuilder.go).
#   2. Calls GET /api/documents/<uid>/fingerprint to read what
#      ragabast has stored for that UID.
#   3. If the fingerprints match: prints "skip" and exits 0.
#   4. Otherwise: prints "ingest" and uploads via
#      POST /api/ingest/file (multipart/form-data with a `file`
#      field).
#
# Exit codes:
#   0  — success (either skipped or ingested)
#   1  — argument error / curl failure / non-2xx response
#   2  — auth failure (401/403)

set -euo pipefail

# ---------- config ----------
RAGABAST_URL="${RAGABAST_URL:-http://localhost:8080}"
AUTH_TOKEN="${AUTH_TOKEN:?AUTH_TOKEN env var is required (export AUTH_TOKEN=...)}"
UID="${1:?usage: $0 <uid> <path/to/markdown-file>}"
FILE="${2:?usage: $0 <uid> <path/to/markdown-file>}"

if [[ ! -r "$FILE" ]]; then
    echo "error: file not readable: $FILE" >&2
    exit 1
fi

# ---------- fingerprint ----------
#
# ragabast's auto-generated fingerprint is hex(sha256(content)).
# When the frontmatter pins an explicit fingerprint: line, that
# value is used verbatim; this script matches the auto-gen case
# (frontmatter without an explicit fingerprint) which is the
# common path for "docbuilder generated this file and never set
# the field itself".
fp_local=$(sha256sum "$FILE" | awk '{print $1}')

# ---------- preflight ----------
#
# Two cases:
#   200 -> body is {"uid":"...","fingerprint":"sha256:...","ingested_at":"..."}
#   404 -> body is the Huma error envelope; fp_stored stays empty.
# We always read the body so jq doesn't choke on the error shape.
response=$(curl -sS \
    -H "Authorization: Bearer ${AUTH_TOKEN}" \
    -w '\n%{http_code}' \
    "${RAGABAST_URL}/api/documents/${UID}/fingerprint")
status=$(printf '%s' "$response" | tail -n1)
body=$(printf '%s' "$response" | sed '$d')

case "$status" in
    200) fp_stored=$(printf '%s' "$body" | jq -r '.fingerprint') ;;
    404) fp_stored="" ;;
    401|403)
        echo "error: auth failed (HTTP $status) — check AUTH_TOKEN" >&2
        exit 2
        ;;
    *)
        echo "error: preflight HTTP $status: $body" >&2
        exit 1
        ;;
esac

# ---------- decide ----------
if [[ -n "$fp_stored" && "$fp_stored" == "$fp_local" ]]; then
    printf 'skip  %s  fingerprint unchanged (%s)\n' "$UID" "$fp_local"
    exit 0
fi

if [[ -z "$fp_stored" ]]; then
    printf 'ingest %s  new document (local fp=%s)\n' "$UID" "$fp_local"
else
    printf 'ingest %s  fingerprint changed (stored=%s local=%s)\n' \
        "$UID" "$fp_stored" "$fp_local"
fi

# ---------- upload ----------
#
# /api/ingest/file takes a multipart/form-data POST with a
# `file` field carrying the raw markdown. The response body
# is {"message":"...","document_id":"...","chunks":N}.
http_status=$(curl -sS -o /tmp/.ragabast-ingest-resp.$$ \
    -w '%{http_code}' \
    -H "Authorization: Bearer ${AUTH_TOKEN}" \
    -F "file=@${FILE};type=text/markdown" \
    "${RAGABAST_URL}/api/ingest/file") || {
    rm -f /tmp/.ragabast-ingest-resp.$$
    echo "error: upload failed (curl exit)" >&2
    exit 1
}

resp_body=$(cat /tmp/.ragabast-ingest-resp.$$)
rm -f /tmp/.ragabast-ingest-resp.$$

case "$http_status" in
    200) printf '%s\n' "$resp_body" | jq . ;;
    401|403)
        echo "error: auth failed (HTTP $http_status) — check AUTH_TOKEN" >&2
        exit 2
        ;;
    413)
        echo "error: document exceeds server.max_ingest_document_bytes (HTTP 413)" >&2
        exit 1
        ;;
    429)
        echo "error: rate-limited (HTTP 429) — try again after Retry-After" >&2
        exit 1
        ;;
    *)
        echo "error: ingest HTTP $http_status: $resp_body" >&2
        exit 1
        ;;
esac
