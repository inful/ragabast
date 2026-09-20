# ragabast Issue-Execution Plan

> Goal: work through architectural issues #9–#44 in dependency order while the
> operator sleeps. Every issue lands with tests, lint clean, and a conventional
> commit. No drive-by changes. No API drift.

---

## Style principle: clean, readable Go

This is the operator's stated preference. Concrete rules for this codebase:

- **Clarity over cleverness.** No one-line tricks that need a comment to
  decode. If a function name requires a sentence to explain, rename it.
- **Functions stay small.** Cyclomatic complexity ≤ 10 (cyclop rule). If a
  function is bigger, split it — that's a sign of mixed concerns.
- **Godoc that explains WHY, not WHAT.** The signature already says what.
  Comments capture the reasoning: trade-offs, the bug this avoids, the
  alternative we rejected.
- **Top-to-bottom readability.** A new contributor should be able to read
  a file from top to bottom and understand the data flow without jumping.
- **Explicit over implicit.** No clever defaulting, no magic string parsing,
  no reflection. If the call sites need to know, name the thing.
- **All comments end with periods.** (godot rule, enforced)
- **No emoji in code.** (AGENTS.md)
- **No naked `os.Setenv` / `os.Chdir` in tests.** Use `t.Setenv` /
  `t.Chdir`. (testifylint rule)
- **Testify assertions:** `require.NoError` for errors,
  `assert.Equal` for values.

---

## Workflow per issue — strict TDD

Every issue follows the same loop. **No exceptions.** The discipline is
non-negotiable: test first, watch it fail, make it pass, refactor clean,
then commit.

### The Red → Green → Refactor cycle

```
   ┌─ Red ─┐    ┌─ Green ─┐    ┌─ Refactor ─┐    ┌─ Commit ─┐
   │ write │ →  │  make   │ →  │  clean up  │ →  │  tests + │
   │ test  │    │ it pass │    │  while     │    │  lint    │
   │       │    │ (min)   │    │  green     │    │  green   │
   └───────┘    └─────────┘    └────────────┘    └──────────┘
```

The test exists **before** any production code touches the file. The
first commit in an issue is either the failing test alone, or the test
plus the minimal code that turns it green — never just code.

### 1. Pre-flight (every issue, no skipping)

```bash
git checkout main && git pull
git checkout -b issue-<N>-<slug>   # e.g. issue-9-fix-delete-n-plus-1
go test ./... -count=1            # MUST be green before any work
golangci-lint run                 # MUST be clean
git status                        # MUST be clean
```

If any of these fail, **stop and fix the baseline first**. Never paper
over a dirty baseline with new changes — it makes regressions
un-findable later.

### 2. Read the issue body

The issue body already has:
- Context
- Current behavior with file references
- Suggested approach
- Acceptance criteria

**Read every word.** The acceptance criteria become the test plan. If
the criteria are ambiguous, that ambiguity blocks the issue — surface
it, don't guess. Each checkbox in the acceptance criteria maps to at
least one test (or one documented manual check).

### 3. RED — write the failing test first

**Bugs** (`bug` label, e.g. #9, #32):
1. Write a test that demonstrates the buggy behavior.
2. Run it; confirm it **fails for the right reason** (the bug, not a
   typo).
3. Commit just the failing test:
   ```
   test: reproduce N+1 in DELETE /api/documents/{id}
   ```
   This is the "I have reproduced the bug" pin.

**Features** (`enhancement`, e.g. #11, #13, #27):
1. Write the acceptance test — call the new public surface and assert
   the new behavior.
2. Run it; confirm it **fails because the surface doesn't exist yet**
   (compile error or assertion failure on the new path).
3. Commit just the failing test:
   ```
   test(api): cover request ID propagation through audit log
   ```

**Refactors** (e.g. #26, #42):
1. Confirm tests already pin the current behavior. If any are missing,
   add them now. They should pass.
2. Commit any newly added characterization tests separately:
   ```
   test: pin current SearchFilters behavior across packages
   ```

### 4. GREEN — minimal implementation

1. Write the **minimum** code that turns the failing test green. No
   speculative generality, no "while I'm here" cleanups.
2. Run the new test — confirm green.
3. Run the full suite — confirm no regressions:
   ```
   go test ./... -count=1
   ```
4. Run the linter — confirm clean:
   ```
   golangci-lint run --fix && golangci-lint run
   ```
5. Commit the implementation:
   ```
   fix(api): drop N+1 list-before-delete in DELETE /api/documents/{id}
   ```

### 5. REFACTOR — clean up while green

1. With tests still green, improve the implementation:
   - Split functions over cyclomatic complexity 10.
   - Extract helpers if a block repeats.
   - Replace inline strings with named constants if used more than once.
   - Tighten godoc to explain *why* (not *what*).
2. Re-run tests + lint after every refactor. **Never refactor with red
   tests.**
3. If the refactor is non-trivial, commit it separately:
   ```
   refactor(api): extract existence check helper for delete handler
   ```

### 6. Verify against acceptance criteria

Walk **every** checkbox in the issue body. Each one is either:
- Backed by a passing test, OR
- A documented manual check with the steps performed.

If any checkbox is unmet, the issue isn't done. Period.

### 7. Commit conventions

Format: `<type>(<scope>): <description>`

| Type | Use for |
|---|---|
| `feat` | New user-facing feature |
| `fix` | Bug fix |
| `refactor` | Internal change, no behavior diff |
| `test` | Tests only (RED step) |
| `docs` | Comments, godoc, README |
| `perf` | Performance, no behavior diff |
| `chore` | Tooling, dependencies, build |

Scopes used in this repo: `api`, `web`, `cli`, `service`, `vector`,
`chunker`, `parser`, `config`, `jobs`, `llm`, `observability`, `security`.

Description rules:
- Imperative mood ("add", "fix", "drop"), no period.
- Lowercase after the colon.
- Reference the issue number in the body, not the subject.
- 72-char soft limit on subject.

**Never amend someone else's commit.** Amend only your own un-pushed
work to keep history clean.

### 8. Pre-commit gate (mandatory, hard fail)

Run these and **do not commit** unless every one passes:

```bash
go test ./... -count=1            # MUST exit 0; -count=1 disables cache
golangci-lint run --fix           # auto-fix what is safe
golangci-lint run                 # MUST exit 0 — zero issues
git status                        # MUST show only intended files
go vet ./...                      # MUST exit 0 — defense in depth
```

If `golangci-lint` flags something the agent's `--fix` can't handle:
fix the code, don't `//nolint` it without a comment explaining why.

If `go test` shows a failure: fix the failure. Do not commit a snapshot
with `--no-verify`. There is no `--no-verify`.

### 9. Push and PR

```bash
git push -u origin issue-<N>-<slug>
gh pr create --title "<type>(<scope>): <subject>" \
             --body "Closes #N

## Summary
- <bullet>
- <bullet>

## Test plan
- [ ] All new code has tests
- [ ] Full suite green: \`go test ./...\`
- [ ] Lint clean: \`golangci-lint run\`"
```

---

## Phases

Issues are ordered so prerequisites come first. Cross-reference comments
on issues #10, #15, #27, #41, #44 capture the strict dependencies; the
phase ordering reflects them.

### Phase 0: Warmup refactors (zero risk)

Issues: **#26, #32, #42**

Pure refactors / cleanups. No behavior change. Safe to batch.

- **#26** Duplicate `SearchFilters` between `internal/service` and
  `internal/vector` — move to leaf package, both packages import it.
- **#32** Remove stale refactor tombstone in `internal/chunker/chunker.go`.
- **#42** Compile-time enforcement that `serviceAPI` matches `Service`.

**Exit criteria:** Three PRs merged. `go test ./...` green. No behavior
change visible from outside the package.

### Phase 1: Observability foundation

Issues: **#11, #15, #21, #27**

The base of every later observability story. **#11 must land first** —
#15 depends on it (cross-ref comment already posted).

- **#11** Request/correlation IDs across HTTP, jobs, LLM.
- **#15** Audit log IP — replaces "unknown" with real IP via request IDs.
- **#21** Migrate `log.Printf` → `log/slog` (structured logging).
- **#27** Doctor command should actually exercise the embedding model.

**Exit criteria:** Every log line has a structured field set. Every
request has an ID echoed in headers, audit lines, and chat-debug logs.
Doctor detects a misconfigured model name.

### Phase 2: Storage hardening

Issues: **#17, #19, #16, #30, #31**

Storage layer hygiene. **#19 (embedding version on chunks) blocks #13
(query cache) downstream**, so it lands here even though cache is later.

- **#17** Async queue cleanup + depth counters — prerequisite for #10, #44.
- **#19** Stamp embedding model on each chunk — prerequisite for #13.
- **#16** Concurrent / batched embedding generation during ingest.
- **#30** Search index path configurable (NFS hazard).
- **#31** Graceful cancellation of in-flight ingest.

**Exit criteria:** Queue has cheap counters. Chunks carry embedding
lineage. Ingest is cancellable. Search index can be moved off the
vector DB path.

### Phase 3: Observability build-out (uses #11, #17)

Issues: **#10, #20, #44**

These were deliberately placed after their prerequisites.

- **#10** `/api/health` reports async queue state.
- **#20** OpenTelemetry / Prometheus metrics.
- **#44** `GET /api/ingest/jobs` — list and filter.

**Exit criteria:** Operator can `curl /api/health` and see queue depth,
worker count, oldest pending age. `/metrics` returns Prometheus text.
`GET /api/ingest/jobs?status=failed` works.

### Phase 4: Security hardening

Issues: **#18, #28, #29**

Order matters: **#28 (multi-token) is the prerequisite for #41 (per-doc
ACLs)** in Phase 8.

- **#18** CSRF protection on form-mounted POSTs.
- **#28** Multi-token auth with optional labels.
- **#29** Validate `docbuilder_base_url` format.

**Exit criteria:** Forms reject cross-origin POSTs without a token. Bearer
auth supports rotation. Config validation catches malformed URLs.

### Phase 5: API surface cleanup

Issues: **#9, #14**

Bug fixes and pagination. Lowest-risk user-facing changes.

- **#9** N+1 fix in `DELETE /api/documents/{id}` — `bug` label.
- **#14** Pagination on `GET /api/documents` + `/documents` UI.

**Exit criteria:** Delete is O(1) not O(N). List endpoint paginates.

### Phase 6: Performance

Issues: **#13**

Query cache. **Depends on #19 (embedding model on chunks)** — the cache
key has to include the model.

- **#13** Cache embeddings + search results with LRU + invalidation.

**Exit criteria:** Config knob works. Hit rate visible in `/api/health`.
Ingest/delete correctly invalidates affected entries.

### Phase 7: New endpoints

Issues: **#24, #25, #38, #35**

Pure additions, no behavior change to existing endpoints.

- **#24** `POST /api/ingest/batch` (NDJSON-friendly).
- **#25** `POST /api/documents/bulk-update`.
- **#38** Date-range filter on search.
- **#35** Webhook callback on async ingest completion.

**Exit criteria:** All four endpoints pass their acceptance criteria
and have tests for the failure paths.

### Phase 8: UX features

Issues: **#12, #22, #23, #34, #39, #40**

User-facing improvements. Can run in any order within the phase.

- **#12** SSE streaming chat.
- **#22** Chat history persistence.
- **#23** `ragabast watch` file watcher.
- **#34** Document preview / chunk inspector UI.
- **#39** Chat transcript export as markdown.
- **#40** i18n — extract user-facing strings.

**Exit criteria:** Each feature ships behind a config flag or feature
flag if it could surprise existing users.

### Phase 9: Architecture refactor

Issues: **#33**

Cross-cutting. May want its own milestone rather than a phase slot.

- **#33** LLM client interface is private to `service` package.

**Exit criteria:** `internal/llm` (or equivalent) exists, OpenAI-shaped
client is an adapter, second provider can be added without an import
cycle.

### Phase 10: Defer (don't pick up)

Issues: **#36, #37, #41**

- **#36** Soft delete — pick up only if there's a concrete user story.
- **#37** Document versioning — epic; split into 3-5 sub-issues before
  working. Out of scope for an autonomous run.
- **#41** Per-document ACLs — explicitly out-of-scope per README. Track
  as future-direction signal only.

---

## Tracking

### Branches

- One branch per issue: `issue-<N>-<slug>`
- Phase branches optional (`phase-3-observability`) for batching
- Squash-merge to `main` per issue, keeping history readable

### Milestones

Set up GitHub milestones mirroring phases:

- `Phase 0: Warmup refactors` (#26, #32, #42)
- `Phase 1: Observability foundation` (#11, #15, #21, #27)
- `Phase 2: Storage hardening` (#17, #19, #16, #30, #31)
- `Phase 3: Observability build-out` (#10, #20, #44)
- `Phase 4: Security hardening` (#18, #28, #29)
- `Phase 5: API surface cleanup` (#9, #14)
- `Phase 6: Performance` (#13)
- `Phase 7: New endpoints` (#24, #25, #38, #35)
- `Phase 8: UX features` (#12, #22, #23, #34, #39, #40)
- `Phase 9: Architecture refactor` (#33)

### Commit cadence

- One commit per logical change.
- Tests + fix in one commit (acceptable) — but commit message must
  reflect that: `feat(observability): add request IDs (tests + impl)`.
- Amend to clean up before pushing.

---

## Pre-flight before any work starts

```bash
# Confirm clean baseline
go test ./... -count=1
golangci-lint run
git status
git log --oneline -5
git branch --show-current
```

If any step fails: **stop**. Don't paper over a dirty baseline with
your changes; you'll never find the regression later.

---

## Stop conditions

Stop work and surface to the operator (do NOT push, do NOT amend):

1. **Acceptance criteria ambiguous** — the issue body doesn't pin what
   "done" looks like. Open a clarifying question on the issue, move to
   the next issue.
2. **Test reproduces a different bug** — the failing test exposed
   something the issue didn't mention. Open a new issue with the
   repro, link to the original, move on.
3. **Change breaks a downstream consumer** — docbuilder, scripts in
   `cmd/`, existing tests. Document the breakage, decide whether to
   fix forward or revert.
4. **Three failed attempts on the same issue** — something is harder
   than the issue suggested. Stop, summarize the attempts, leave the
   branch in a known state (commit or stash), move to the next issue.
5. **Cumulative test failures across unrelated packages** — likely
   the local baseline wasn't clean. Stop, fix the baseline, then
   resume.
6. **Cyclomatic complexity warning persists after 2 refactor passes**
   — the function is doing too much. Note the gap, accept the
   complexity or split into a follow-up issue.
7. **`golangci-lint` issue persists after the second refactor attempt
   with no `//nolint` shortcut** — the fix isn't structural. Stop,
   split the offending function into smaller pieces in a follow-up
   issue, and decide whether to ship with the lint warning temporarily
   or revert.
8. **Test would require mocking more than 2 internal interfaces** —
   the API surface is too tightly coupled for the test to be honest.
   Open a refactor issue first, move on.
9. **Cannot reproduce the bug at all** — the issue is a phantom. Open
   a comment on the issue asking for repro steps, do not commit a
   speculative fix.

---

## Daily / end-of-session summary

When stopping (after a phase, or because of a stop condition), leave:

1. **Phase status** — which phases complete, which in-progress, which
   skipped and why.
2. **Open PRs** — list of branches pushed and PRs opened with links.
3. **Open issues** — anything surfaced (new bugs, ambiguous criteria)
   with links.
4. **Local state** — uncommitted changes (with rationale for not
   committing), branch names, worktrees to clean up.

The operator reviews this in the morning and either:
- Approves and merges the PRs
- Requests changes
- Resumes the next phase

---

## Estimated scope for an overnight run

Phases 0–5 cover 18 issues, roughly:
- 30–90 minutes per issue with TDD
- Lower estimate: 9 hours
- Upper estimate: 27 hours

Reasonable for an unattended overnight run if no issue drags. Phase 6
onward can be picked up in the next session.

The plan stops at Phase 5 by default; the operator can extend to Phase 6+
in the morning based on progress.
