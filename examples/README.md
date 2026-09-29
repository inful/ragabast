# Examples

Small, self-contained scripts showing how to integrate with
ragabast from external services. Each example is a single
file with no build step (or a single `go build`).

## ingest-with-preflight.sh

A bash script that demonstrates the full client loop using
two endpoints together:

- `GET /api/documents/{uid}/fingerprint` — preflight check
- `POST /api/ingest/file` — multipart upload

The script reads the `fingerprint:` value from the
document's YAML frontmatter (the same value ragabast
stored at ingest time — see the **frontmatter-is-
authoritative** contract in the main README's preflight
section), asks ragabast for the stored fingerprint, and
only uploads when they disagree. This is the recommended
pattern for ingest pipelines that re-scan a docbuilder
tree on a schedule — most invocations become a single
cheap GET instead of a multi-MB upload.

**Requires:** `bash`, `curl`, `awk`, `jq`.

**Run:**

```bash
export RAGABAST_URL=https://ragabast.example.com
export AUTH_TOKEN=...
./ingest-with-preflight.sh adr-001 path/to/adr-001.md
```

The script prints one of:

- `skip  <uid>  fingerprint unchanged (...)` — nothing to do
- `ingest <uid>  new document (...)` — first ingest
- `ingest <uid>  fingerprint changed (...)` — re-ingest
- The full JSON response from `/api/ingest/file` on success

Exit codes:

- `0` — success (skipped or ingested)
- `1` — argument / curl / non-2xx response
- `2` — auth failure (HTTP 401/403)

**Why read the frontmatter instead of computing the
fingerprint locally?** Because docbuilder is the canonical
source of the algorithm (today: `sha256(body)` where body
is the markdown content minus frontmatter delimiters and
the fingerprint line itself). The client would have to
mirror that algorithm AND parse the frontmatter to strip
the fingerprint line before hashing — both of which the
frontmatter already does for us, so reading the value is
strictly simpler and avoids drift if docbuilder changes
the algorithm in a future release. If the frontmatter has
no `fingerprint:` line (the file was hand-written or
docbuilder never ran on it), the script skips preflight
and uploads unconditionally — the ragabast parser will
auto-generate a fingerprint at ingest time.

## Adding new examples

Keep new examples in this directory, one per file, named
after what they demonstrate. Match the conventions in
`ingest-with-preflight.sh`:

- `set -euo pipefail` at the top
- Config from env vars (no hard-coded URLs/tokens)
- UID + file path as positional args
- Status output on stderr, data on stdout
- Distinct exit codes per failure class

If you add a Go example, it should live in a subdirectory
with its own `go.mod` (so `go build ./examples/foo` works
without affecting the main module) — not yet created
because the only example so far is bash.
