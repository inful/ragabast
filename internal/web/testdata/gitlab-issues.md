# Ingesting GitLab issues

Ragabast is a **pure receiver** of ingestion requests. This document
shows how to push GitLab issues into ragabast from a small bash
sender — the kind of script an operator might run from cron, a CI
job, or a webhook receiver.

The ragabast side accepts a single endpoint:

```
POST /api/ingest/gitlab/issue
Content-Type: application/json
Authorization: Bearer <ragabast bearer token>
```

The body is a verbatim GitLab issue JSON envelope, optionally with
inline notes. See the OpenAPI docs (`/docs`) for the schema, or the
test fixtures in `internal/gitlab/payload_test.go` for worked
examples copied from the GitLab docs.

## Prerequisites

The sender needs three pieces of information:

| Variable | Meaning | Example |
|---|---|---|
| `GITLAB_TOKEN` | GitLab personal access token, `api` or `read_api` scope | `glpat-xxxxxxxxxxxx` |
| `GITLAB_URL` | GitLab base URL (no trailing slash) | `https://gitlab.com` |
| `PROJECT_PATH` | URL-encoded project path | `group%2Fproject` |
| `RAGABAST_URL` | Ragabast base URL | `http://localhost:9200` |
| `RAGABAST_TOKEN` | Ragabast bearer token (sent as `Authorization: Bearer …` — see Authentication below) | `ragabast-secret` |

The GitLab token can be issued at *User Settings → Access Tokens* in
the GitLab web UI. The `read_api` scope is sufficient — ragabast only
reads.

## Example 1 — push a single issue (no comments)

The simplest flow. Useful for testing the wiring without walking a
whole project.

```bash
#!/usr/bin/env bash
set -euo pipefail

: "${GITLAB_TOKEN:?GITLAB_TOKEN is required}"
: "${GITLAB_URL:?GITLAB_URL is required}"
: "${PROJECT_PATH:?PROJECT_PATH is required}"
: "${RAGABAST_URL:?RAGABAST_URL is required}"
: "${RAGABAST_TOKEN:?RAGABAST_TOKEN is required}"

ISSUE_IID="${1:?usage: $0 <issue-iid>}"

# Fetch the issue.
ISSUE_JSON=$(curl -fsSL \
  -H "PRIVATE-TOKEN: ${GITLAB_TOKEN}" \
  "${GITLAB_URL}/api/v4/projects/${PROJECT_PATH}/issues/${ISSUE_IID}")

# Send to ragabast. The envelope is {issue: <raw>, "path_with_namespace":
# <decoded slug>}. The notes array is empty.
SLUG=$(printf '%s' "${PROJECT_PATH}" | python3 -c 'import sys, urllib.parse; print(urllib.parse.unquote(sys.stdin.read()))')

jq -n \
  --argjson issue "${ISSUE_JSON}" \
  --arg slug "${SLUG}" \
  '{issue: $issue, path_with_namespace: $slug, include_notes: false, include_system_notes: false, notes: []}' \
  | curl -fsSL -X POST \
      -H "Authorization: Bearer ${RAGABAST_TOKEN}" \
      -H "Content-Type: application/json" \
      --data-binary @- \
      "${RAGABAST_URL}/api/ingest/gitlab/issue"
```

Run it as:

```sh
./gitlab-push.sh 42
# {"message":"Document ingested successfully","document_id":"gitlab:group/project:42","chunks":3}
```

The `document_id` in the response is the ragabast UID — `gitlab:<slug>:<iid>`.
The same UID reappears in `/api/documents` and `/api/search`, so a
follow-up `curl ${RAGABAST_URL}/api/documents/gitlab:group/project:42`
fetches the ingest metadata.

## Example 2 — walk every open issue, with comments

The bulk flow. Two API calls per issue (the issue, then its notes),
then one POST to ragabast. The script below handles pagination
against GitLab's `per_page=100` default and yields each issue to
the ragabast endpoint. Comments are filtered for `system: true`
records (the "closed", "changed title to X" entries) by default;
set `include_system_notes: true` if you want them embedded.

```bash
#!/usr/bin/env bash
set -euo pipefail

: "${GITLAB_TOKEN:?GITLAB_TOKEN is required}"
: "${GITLAB_URL:?GITLAB_URL is required}"
: "${PROJECT_PATH:?PROJECT_PATH is required}"
: "${RAGABAST_URL:?RAGABAST_URL is required}"
: "${RAGABAST_TOKEN:?RAGABAST_TOKEN is required}"

SLUG=$(printf '%s' "${PROJECT_PATH}" | python3 -c 'import sys, urllib.parse; print(urllib.parse.unquote(sys.stdin.read()))')

page=1
while :; do
  # Fetch a page of issues (state=opened by default).
  PAGE_JSON=$(curl -fsSL \
    -H "PRIVATE-TOKEN: ${GITLAB_TOKEN}" \
    "${GITLAB_URL}/api/v4/projects/${PROJECT_PATH}/issues?page=${page}&per_page=100")

  # Empty page → done.
  COUNT=$(echo "${PAGE_JSON}" | jq 'length')
  [ "${COUNT}" -eq 0 ] && break

  # For each issue on this page, fetch its notes and POST.
  for row in $(echo "${PAGE_JSON}" | jq -r '.[] | @base64'); do
    ISSUE_IID=$(echo "${row}" | base64 -d | jq -r '.iid')

    NOTES_JSON=$(curl -fsSL \
      -H "PRIVATE-TOKEN: ${GITLAB_TOKEN}" \
      "${GITLAB_URL}/api/v4/projects/${PROJECT_PATH}/issues/${ISSUE_IID}/notes?per_page=100")

    echo "${row}" | base64 -d | jq \
      --argjson notes "${NOTES_JSON}" \
      --arg slug "${SLUG}" \
      '{issue: ., path_with_namespace: $slug, include_notes: true, include_system_notes: false, notes: $notes}' \
      | curl -fsSL -X POST \
          -H "Authorization: Bearer ${RAGABAST_TOKEN}" \
          -H "Content-Type: application/json" \
          --data-binary @- \
          "${RAGABAST_URL}/api/ingest/gitlab/issue" \
      | jq -r '"ingested issue #\(.document_id) -> \(.message)"'
  done

  page=$((page + 1))
done
```

Run it as:

```sh
./gitlab-sync-all-open.sh
# ingested issue #gitlab:group/project:42 -> Document ingested successfully
# ingested issue #gitlab:group/project:43 -> Document ingested successfully
# ...
```

For closed issues, change `state=opened` to `state=all` (or
`state=closed` if you only want resolved ones).

## Example 3 — one-shot sync from cron

The previous example is a normal bash script — wrap it in a cron
entry, a CI job, or a systemd timer. The script is idempotent: a
second run produces no-ops (the ragabast fingerprint-based dedupe
in `Service.IngestDocument` skips identical content) and replaces
out-of-date content (the same fingerprint-check path detects
content drift).

A typical nightly sync:

```cron
# /etc/cron.d/ragabast-gitlab
30 1 * * * ragabast /opt/ragabast/contrib/gitlab-sync-all-open.sh
```

The script also works against a self-hosted GitLab — just set
`GITLAB_URL=https://gitlab.example.com` and the same code paths
apply.

## Operational notes

### System-note log line

When default-on filtering drops records, the ragabast handler logs:

```
gitlab: filtered N system notes from issue URL (iid=N)
```

If you expect to see N comments in chat and don't, grep your
ragabast logs for that line.

### The `gitlab-issue` tag

Every ingested issue carries two tags the chat / search surfaces
already see:

- `gitlab-issue` — applied to every doc issue, regardless of project
- `gitlab:<slug>` — scoped to one project

These are the hooks a future "filter chat by source kind" PR will
hang on. Today they're just tags — ragabast doesn't filter on them
yet.

### Re-ingest is safe

The same UID re-POSTed is a no-op when the issue hasn't changed
(content fingerprint matches), and an in-place update when it has
(the fingerprint path detects drift and rewrites chunks). It's
safe to run the bulk sync as often as your schedule calls for.

### Confidentiality

`confidential: true` issues land in ragabast just like any other.
The flag becomes a tag, so a future filter can hide them. Today
ragabast trusts the sender.

### Authentication on the ragabast side

The same bearer-token middleware that protects `/api/ingest/raw`
protects `/api/ingest/gitlab/issue`. Ragabast reads `Authorization:
Bearer <token>` (or the legacy `Authorization: Token <token>` form
— both are accepted, see `internal/web/auth.go::matchBearer`).
GitLab's own API uses a different `PRIVATE-TOKEN:` header; the two
are unrelated and easy to confuse because both involve a "token" header.
If you've enabled bearer tokens on ragabast (see `auth.tokens` in
`config.yml`), set `RAGABAST_TOKEN` to one of them. If auth is
disabled, the endpoint is open.

## Reference

- [GitLab Issues API](https://docs.gitlab.com/api/issues/)
- [GitLab Notes API](https://docs.gitlab.com/api/notes/)
- `internal/gitlab/payload.go` — the on-the-wire schema
- `internal/web/huma_ingest_gitlab.go` — the handler