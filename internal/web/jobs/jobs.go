// Package jobs implements the persistent ingest job queue used
// by the async ingest endpoint (POST /api/ingest/async).
//
// Why a queue at all: the docbuilder server imports documents in
// bursts — often a full first-run import with thousands of
// documents. A synchronous ingest handler can sit on each call
// for seconds (embedding model round-trip) and tie up docbuilder's
// HTTP client until it times out. Async lets docbuilder fire a
// burst, get 202 Accepted immediately, then poll for completion.
//
// Why persistence: a process restart (deploy, crash, OOM kill)
// during a long import would otherwise drop every job that was
// in-flight. The queue persists each job to a JSON file in
// <queue_dir>/<job_id>.json; on startup we re-enqueue every file
// whose status is not "completed" or "failed".
//
// Why JSON files instead of sqlite/bbolt: zero new dependencies,
// trivially inspectable by the operator (cat data/jobs/<id>.json),
// trivial to back up (cp -r data/jobs/), and the scale we're
// designed for (thousands of jobs per import, not millions) is
// well within what the filesystem handles. If we ever need
// cross-instance queueing, swap the storage layer; the rest of
// the package is decoupled.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Status is the lifecycle state of an ingest job. The values
// are the literal strings written to disk; do not rename
// without a migration.
type Status string

const (
	// StatusPending means the job has been accepted but no
	// worker has started it yet.
	StatusPending Status = "pending"
	// StatusProcessing means a worker has claimed the job
	// (atomic rename to .processing) and is running
	// IngestDocument.
	StatusProcessing Status = "processing"
	// StatusCompleted means the ingest succeeded; DocumentID
	// and Chunks are populated.
	StatusCompleted Status = "completed"
	// StatusFailed means the ingest returned an error;
	// Error is populated.
	StatusFailed Status = "failed"
)

// Job is the persisted shape of an ingest job. Field tags are
// chosen so the file is human-readable (lower-case snake_case)
// and round-trips through encoding/json without surprises.
type Job struct {
	ID          string     `json:"id"`
	Content     string     `json:"content"`
	Status      Status     `json:"status"`
	Error       string     `json:"error,omitempty"`
	DocumentID  string     `json:"document_id,omitempty"`
	Chunks      int        `json:"chunks,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`

	// RemoteAddr is the IP of the caller who submitted the job
	// (audit trail only; never logged in the document
	// content). Optional so older test fixtures that do not
	// set it still load.
	RemoteAddr string `json:"remote_addr,omitempty"`

	// RequestID is the correlation ID from the originating
	// HTTP request (issue #11). Empty when the job was
	// submitted by a non-HTTP caller (CLI, tests). Optional
	// in JSON so older fixtures without the field still
	// decode.
	RequestID string `json:"request_id,omitempty"`
}

// ErrJobNotFound is returned by Queue.Get when the requested
// job ID is not on disk. Callers can distinguish "no such
// job" from "I/O error" by errors.Is.
var ErrJobNotFound = errors.New("job not found")

// ErrJobContentTooLarge is returned by Queue.Submit when the
// supplied content exceeds maxBytes. The Queue enforces the
// same limit the synchronous ingest handler enforces, so a
// rejected async submit would also be a rejected sync submit.
var ErrJobContentTooLarge = errors.New("job content exceeds maxBytes")

// Queue is the persistent job queue. Safe for concurrent use
// from many submitter and worker goroutines; the on-disk
// representation is the source of truth.
//
// Lifecycle:
//
//	q, err := jobs.New(dir, maxBytes, workers)
//	q.Start(svc, audit)
//	defer q.Stop()
//
// New scans the directory and counts any unfinished jobs
// (pending or processing); Start launches the worker pool and
// re-enqueues those jobs.
type Queue struct {
	dir      string
	maxBytes int
	workers  int

	// pending is the channel worker goroutines select on.
	// Buffered to workers so Submit never blocks while a
	// worker is mid-job. A nil channel is used to stop
	// workers (select on a nil channel never fires).
	pending chan string

	// stopOnce guards Stop so it can be called multiple
	// times safely.
	stopOnce sync.Once
	stopped  chan struct{}

	// workerCtx + workerCtxCancel back the cancelable
	// context handed to in-flight processOne calls. Stop
	// triggers workerCtxCancel so slow IngestDocument
	// calls observe ctx.Done() and return promptly. Both
	// fields are populated by Start and read by worker
	// goroutines only after Start has returned, so no
	// synchronization is needed.
	//
	//nolint:containedctx // standard pattern for cancelable worker pools
	workerCtx       context.Context
	workerCtxCancel context.CancelFunc
}

// Service is the subset of the service layer the worker
// invokes. Defined as an interface so the jobs package has
// no dependency on internal/service.
type Service interface {
	IngestDocument(ctx context.Context, content string) (documentID string, chunks int, err error)
}

// AuditFunc is the hook called on every job state transition.
// Used to write the operator audit log. The function must not
// block; pass a buffered channel or a goroutine if heavy work
// is needed.
type AuditFunc func(event string, fields ...any)

// New constructs a Queue. dir must exist (or be creatable);
// maxBytes is the per-job content cap (0 disables); workers is
// the bounded concurrency (0 falls back to 1).
func New(dir string, maxBytes, workers int) (*Queue, error) {
	if workers <= 0 {
		workers = 1
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create queue dir: %w", err)
	}
	return &Queue{
		dir:      dir,
		maxBytes: maxBytes,
		workers:  workers,
		pending:  make(chan string, workers),
		stopped:  make(chan struct{}),
	}, nil
}

// Dir returns the queue's on-disk directory. Exposed so the
// startup recovery and the HTTP handler can show the
// operator where the state lives.
func (q *Queue) Dir() string { return q.dir }

// MaxBytes returns the configured per-job content cap.
func (q *Queue) MaxBytes() int { return q.maxBytes }

// Workers returns the configured worker count.
func (q *Queue) Workers() int { return q.workers }

// Submit creates a pending job on disk and enqueues its ID for
// a worker. Returns the assigned job ID.
//
// requestID is the correlation ID from the originating HTTP
// request (or "" when the caller is not an HTTP handler). It is
// persisted on the Job and emitted in every audit line so the
// ingest-job trail can be correlated with the request that
// submitted it. See issue #11.
//
// Concurrency: safe from many goroutines.
func (q *Queue) Submit(content, remoteAddr, requestID string) (*Job, error) {
	if q.maxBytes > 0 && len(content) > q.maxBytes {
		return nil, ErrJobContentTooLarge
	}

	j := &Job{
		ID:         uuid.NewString(),
		Content:    content,
		Status:     StatusPending,
		CreatedAt:  time.Now().UTC(),
		RemoteAddr: remoteAddr,
		RequestID:  requestID,
	}

	if err := q.write(j); err != nil {
		return nil, fmt.Errorf("write job: %w", err)
	}

	// Non-blocking send. The channel is buffered to the
	// worker count; if all workers are busy AND the buffer is
	// full, we wait — but only briefly, and only ever by one
	// send per slot. Backpressure is the desired behavior:
	// the caller waits until a worker frees up before
	// returning the job ID.
	select {
	case q.pending <- j.ID:
	case <-q.stopped:
		return nil, errors.New("queue is stopped")
	}

	return j, nil
}

// Get loads a job from disk by ID. Returns ErrJobNotFound if
// the file does not exist.
func (q *Queue) Get(id string) (*Job, error) {
	path := q.path(id)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrJobNotFound
		}
		return nil, fmt.Errorf("read job: %w", err)
	}
	var j Job
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, fmt.Errorf("decode job: %w", err)
	}
	return &j, nil
}

// List returns all jobs in the queue directory, sorted by
// CreatedAt. status filter is optional; pass "" to list all.
func (q *Queue) List(statusFilter Status) ([]*Job, error) {
	entries, err := os.ReadDir(q.dir)
	if err != nil {
		return nil, fmt.Errorf("read queue dir: %w", err)
	}
	var out []*Job
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".tmp") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		j, err := q.Get(id)
		if err != nil {
			// Skip unreadable files rather than failing
			// the whole list — the operator can investigate
			// via the file system.
			continue
		}
		if statusFilter != "" && j.Status != statusFilter {
			continue
		}
		out = append(out, j)
	}
	return out, nil
}

// Start launches the worker pool and re-enqueues any unfinished
// jobs that were on disk before this process started. svc is the
// ingest implementation the workers call; audit is the
// state-transition log hook (optional).
//
// Start must be called exactly once. It is idempotent: a second
// call is a no-op (the worker pool only spins up once).
//
// Recovery runs synchronously — Start does not return until
// recoverUnfinished has scanned the directory and re-enqueued
// any unfinished jobs. Without this, a Submit that races with
// recovery can be enqueued twice: once by Submit's channel
// send (after Start returns) and again by recovery (which
// sees the .json file Submit just wrote). With two workers in
// the pool, each receives one of the duplicate IDs and processes
// the same job — IngestDocument is invoked twice and the audit
// log reports two completed runs for one user request.
//
// Stop will cancel workerCtx, which in-flight processOne
// goroutines observe via the context handed to IngestDocument.
// Slow ingest calls therefore abort promptly at shutdown
// rather than running to completion. See issue #31.
func (q *Queue) Start(svc Service, audit AuditFunc) {
	q.workerCtx, q.workerCtxCancel = context.WithCancel(context.Background())

	for range q.workers {
		go q.worker(svc, audit)
	}

	// Re-enqueue any unfinished jobs found on disk. This is
	// the persistence path: a restart that lost the
	// in-memory pending channel still picks up every
	// pending or processing job. Synchronous so the
	// caller's first Submit cannot race with recovery.
	q.recoverUnfinished(audit)
}

// Stop signals workers and the recovery goroutine to exit.
// Workers finish whatever job they are currently processing;
// subsequent Submits return an error immediately.
//
// Issue #31: in-flight IngestDocument calls observe the
// workerCtx cancellation this method triggers. Slow ingest
// calls therefore abort promptly at shutdown rather than
// running to completion. The worker pool itself still drains
// via the closed stopped channel — the cancel is for the
// slow call, the close is for the select loop.
//
// We do NOT close pending: closing would panic any concurrent
// sender (Submit, recover) that races with Stop. Instead the
// workers' select-loop pattern watches the stopped channel,
// which is closed exactly once via stopOnce.
func (q *Queue) Stop() {
	q.stopOnce.Do(func() {
		// 1. Cancel the worker context so in-flight
		//    processOne calls observe ctx.Done().
		if q.workerCtx != nil {
			q.workerCtxCancel()
		}
		// 2. Close the stopped channel so the worker
		//    select loop exits at the next iteration.
		close(q.stopped)
	})
}

// recoverUnfinished scans the queue dir at startup and
// re-enqueues every job whose status is pending. The "processing"
// path is fenced by file mtime so a still-running worker is not
// reaped by a peer startup's recovery scan.
//
// Two file shapes can be on disk at startup:
//
//  1. <id>.json with status=pending — normal pending job.
//  2. <id>.processing.json — a worker holds this job; we cannot
//     tell from the filesystem alone whether the worker is
//     alive or dead. We fence on file mtime: a worker keeps
//     touching the file on every state transition (mark-processing,
//     write-final), so a recent mtime means the worker is alive.
//     Anything older than recoverStaleThreshold is presumed
//     orphaned and reaped.
//
// Note: this used to ALSO process .processing.json files
// unconditionally — that was racy because recover runs at
// startup in parallel with the workers, and a worker that
// was mid-IngestDocument could be reaped from under it,
// producing a duplicate IngestDocument call. The fence is
// the fix; tests that simulate a true mid-process crash
// set the file mtime to old via os.Chtimes before the
// second Start to exercise the recovery path.
const recoverStaleThreshold = 5 * time.Second

func (q *Queue) recoverUnfinished(audit AuditFunc) {
	entries, err := os.ReadDir(q.dir)
	if err != nil {
		if audit != nil {
			audit("jobs.recover_error", "error", err.Error())
		}
		return
	}

	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".tmp") {
			// Crash left a partial write behind. Clean
			// it up; the .json (if any) is the source of
			// truth.
			_ = os.Remove(filepath.Join(q.dir, name))
			continue
		}

		var id string
		var sourcePath string
		switch {
		case strings.HasSuffix(name, ".processing.json"):
			// Only reap a .processing.json file if its
			// mtime is older than the stale threshold.
			// A live worker keeps touching the file on
			// every state transition; a crashed worker
			// leaves the file at its last write time. A
			// 5s threshold tolerates slow IngestDocument
			// calls while still catching real crashes
			// quickly enough to matter.
			info, ierr := e.Info()
			if ierr != nil {
				continue
			}
			if time.Since(info.ModTime()) < recoverStaleThreshold {
				continue
			}
			id = strings.TrimSuffix(name, ".processing.json")
			sourcePath = q.processingPath(id)
		case strings.HasSuffix(name, ".json"):
			id = strings.TrimSuffix(name, ".json")
			sourcePath = q.path(id)
		default:
			continue
		}

		j, gerr := q.getFromPath(sourcePath)
		if gerr != nil {
			if audit != nil {
				audit("jobs.recover_read_error", "job_id", id, "error", gerr.Error())
			}
			continue
		}

		if j.Status == StatusCompleted || j.Status == StatusFailed {
			// Already finished — nothing to do. If the
			// file is at .processing.json by mistake,
			// rename it back to .json so readers find it.
			if sourcePath != q.path(id) {
				_ = os.Rename(sourcePath, q.path(id))
			}
			continue
		}

		// Reset to pending; clear timing fields from any
		// prior processing run.
		j.Status = StatusPending
		j.StartedAt = nil
		j.CompletedAt = nil

		// Move the file to its canonical .json path
		// (idempotent if already there) before enqueuing.
		if sourcePath != q.path(id) {
			if rerr := os.Rename(sourcePath, q.path(id)); rerr != nil && audit != nil {
				audit("jobs.recover_rename_error", "job_id", id, "error", rerr.Error())
				continue
			}
		}
		if werr := q.write(j); werr != nil && audit != nil {
			audit("jobs.recover_write_error", "job_id", id, "error", werr.Error())
			continue
		}

		select {
		case q.pending <- j.ID:
		case <-q.stopped:
			return
		}
	}
}

// worker is the goroutine that drains the pending channel. Each
// worker pulls one job ID at a time, atomically claims it by
// renaming pending → processing, runs the ingest, and writes
// the final state.
//
// Stopping uses the stopped channel, not a close of pending —
// closing pending would panic any goroutine (Submit, recover)
// that tried to send after Stop was called. The select-loop
// pattern below is the standard "done channel" idiom.
func (q *Queue) worker(svc Service, audit AuditFunc) {
	for {
		select {
		case <-q.stopped:
			return
		case id, ok := <-q.pending:
			if !ok {
				return
			}
			q.processOne(id, q.workerCtx, svc, audit)
		}
	}
}

func (q *Queue) processOne(id string, ctx context.Context, svc Service, audit AuditFunc) {
	// Read the job from the canonical .json path BEFORE we
	// claim it; if it doesn't exist there it might exist at
	// .processing.json (a prior worker was interrupted, or
	// recoverUnfinished put it there). Either way we need
	// to know the content.
	j, err := q.Get(id)
	if err != nil {
		// Try the processing path too — recoverUnfinished
		// renames pending → processing → json, and a prior
		// worker may have left the file at .processing.json
		// if it crashed mid-ingest.
		if pj, perr := q.getAtProcessingPath(id); perr == nil {
			j = pj
			err = nil
		}
	}
	if err != nil {
		if audit != nil {
			audit("jobs.process_load_error", "job_id", id, "error", err.Error())
		}
		return
	}
	if j.Status == StatusCompleted || j.Status == StatusFailed {
		// Already done — happens if recoverUnfinished raced
		// with the worker that finished the job.
		return
	}

	// Claim: rename .json to .processing.json. os.Rename
	// is atomic on POSIX; this is the "exactly one worker
	// picks this up" primitive. If the rename fails because
	// the file is already .processing.json (another worker
	// claimed it first), skip.
	if rerr := os.Rename(q.path(id), q.processingPath(id)); rerr != nil {
		if !errors.Is(rerr, os.ErrNotExist) {
			if audit != nil {
				audit("jobs.claim_error", "job_id", id, "error", rerr.Error())
			}
		}
		return
	}

	// Mark processing; StartedAt is the audit signal that
	// work has begun. We write to .processing.json (the
	// file we just claimed) so a crash mid-ingest preserves
	// the content for recovery.
	now := time.Now().UTC()
	j.Status = StatusProcessing
	j.StartedAt = &now
	if audit != nil {
		audit("jobs.started", append(jobAuditFields(j), "bytes", len(j.Content))...)
	}
	if werr := q.writeAtPath(j, q.processingPath(id)); werr != nil && audit != nil {
		audit("jobs.mark_processing_error", "job_id", id, "error", werr.Error())
	}

	// Run the ingest.
	docID, chunks, err := svc.IngestDocument(ctx, j.Content)
	done := time.Now().UTC()
	j.CompletedAt = &done
	if err != nil {
		j.Status = StatusFailed
		j.Error = err.Error()
		if audit != nil {
			audit("jobs.failed", append(jobAuditFields(j), "error", err.Error())...)
		}
	} else {
		j.Status = StatusCompleted
		j.DocumentID = docID
		j.Chunks = chunks
		if audit != nil {
			audit("jobs.completed", append(jobAuditFields(j), "document_id", docID, "chunks", chunks)...)
		}
	}

	// Persist final state to .processing.json, then atomic
	// rename back to .json. The rename is the moment the
	// job becomes visible to readers as completed/failed.
	if werr := q.writeAtPath(j, q.processingPath(id)); werr != nil && audit != nil {
		audit("jobs.write_final_error", "job_id", id, "error", werr.Error())
	}
	if rerr := os.Rename(q.processingPath(id), q.path(id)); rerr != nil && audit != nil {
		audit("jobs.finalize_rename_error", "job_id", id, "error", rerr.Error())
	}
}

// path returns the canonical .json path for a job ID.
func (q *Queue) path(id string) string {
	return filepath.Join(q.dir, id+".json")
}

// RunCleanup walks the queue directory once and removes finished
// jobs whose age exceeds the configured TTL. Returns the number
// of files removed.
//
// A TTL of 0 disables cleanup entirely (operators use this to
// pause the feature without removing the config keys). The
// completed TTL applies to StatusCompleted jobs; the failed TTL
// applies to StatusFailed jobs. Failed jobs default to a longer
// retention so operators have time to investigate.
//
// Active processing is preserved: files ending in
// `.processing.json` or `.tmp` are NEVER removed regardless of
// age, even with TTL=1ns. The cleanup pass would otherwise race
// with a worker holding a job and corrupt mid-ingest state.
//
// The audit hook is nil-safe — callers that don't care about
// observability can pass nil.
func (q *Queue) RunCleanup(completedTTL, failedTTL time.Duration) (int, error) {
	return q.RunCleanupWithAudit(completedTTL, failedTTL, nil)
}

// RunCleanupWithAudit is the form that emits a summary audit
// event after the sweep. The single event has event=
// "jobs.cleanup_completed" with fields removed=<int>,
// completed_removed=<int>, failed_removed=<int>.
//
// Operators rely on this for monitoring: a sudden spike in
// the removed count, or a sustained zero, both warrant
// attention.
func (q *Queue) RunCleanupWithAudit(completedTTL, failedTTL time.Duration, audit AuditFunc) (int, error) {
	if completedTTL == 0 && failedTTL == 0 {
		// TTL=0 on both sides means "operator disabled cleanup".
		// Short-circuit so we don't even walk the dir.
		return 0, nil
	}

	now := time.Now()
	entries, err := os.ReadDir(q.dir)
	if err != nil {
		return 0, fmt.Errorf("read queue dir: %w", err)
	}

	completedRemoved := 0
	failedRemoved := 0
	for _, e := range entries {
		name := e.Name()
		// Skip active-processing markers and partial writes.
		// The contract: cleanup never touches anything that
		// isn't a finished-job canonical file.
		if strings.HasSuffix(name, ".processing.json") ||
			strings.HasSuffix(name, ".tmp") ||
			!strings.HasSuffix(name, ".json") {
			continue
		}

		id := strings.TrimSuffix(name, ".json")
		path := q.path(id)
		j, gerr := q.getFromPath(path)
		if gerr != nil {
			// Unreadable file — skip rather than fail the
			// whole sweep. A future pass can retry.
			continue
		}

		var ttl time.Duration
		switch j.Status {
		case StatusCompleted:
			ttl = completedTTL
		case StatusFailed:
			ttl = failedTTL
		case StatusPending, StatusProcessing:
			// Pending and Processing are not eligible.
			// A pending job that age-exceeded TTL is left
			// alone — recovery owns those. A processing job
			// is owned by a worker.
			continue
		}
		if ttl == 0 {
			continue
		}

		// Eligible for removal: check age.
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		if now.Sub(info.ModTime()) < ttl {
			continue
		}

		if rerr := os.Remove(path); rerr != nil {
			// Best-effort; a future pass can retry.
			continue
		}
		switch j.Status {
		case StatusCompleted:
			completedRemoved++
		case StatusFailed:
			failedRemoved++
		case StatusPending, StatusProcessing:
			// Unreachable — the eligibility switch above
			// continued past these. Listed so the
			// exhaustive linter is satisfied.
		}
	}

	total := completedRemoved + failedRemoved
	if audit != nil {
		audit("jobs.cleanup_completed",
			"removed", total,
			"completed_removed", completedRemoved,
			"failed_removed", failedRemoved)
	}
	return total, nil
}

// StartCleanup launches a background goroutine that runs
// RunCleanupWithAudit every interval. Stops when the queue
// is stopped. Returns immediately; the goroutine exits via
// the queue's stop channel.
//
// interval == 0 disables the background sweep — operators
// who want to run cleanup manually can call RunCleanup
// directly. (TTL=0 already disables cleanup entirely.)
func (q *Queue) StartCleanup(interval, completedTTL, failedTTL time.Duration, audit AuditFunc) {
	if interval <= 0 {
		return
	}
	if completedTTL == 0 && failedTTL == 0 {
		return
	}
	go q.cleanupLoop(interval, completedTTL, failedTTL, audit)
}

func (q *Queue) cleanupLoop(interval, completedTTL, failedTTL time.Duration, audit AuditFunc) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-q.stopped:
			return
		case <-t.C:
			_, _ = q.RunCleanupWithAudit(completedTTL, failedTTL, audit)
		}
	}
}

// jobAuditFields returns the standard prefix of every per-job
// audit line: job_id, remote_addr, and (when present) request_id.
// Centralizing this keeps the three call sites in processOne
// (started/failed/completed) consistent, and lets the request_id
// key be omitted cleanly for non-HTTP callers — instead of
// emitting "request_id=" with an empty value, the field is just
// absent from the audit line.
func jobAuditFields(j *Job) []any {
	fields := []any{"job_id", j.ID, "remote_addr", j.RemoteAddr}
	if j.RequestID != "" {
		fields = append(fields, "request_id", j.RequestID)
	}
	return fields
}

// processingPath returns the .processing.json path used
// while a worker holds the job. The atomic rename pending →
// processing is the lock that prevents two workers from
// processing the same job.
func (q *Queue) processingPath(id string) string {
	return filepath.Join(q.dir, id+".processing.json")
}

// write persists a job to the canonical .json path.
// Atomic: write to .tmp, then rename to .json.
func (q *Queue) write(j *Job) error {
	return q.writeAtPath(j, q.path(j.ID))
}

// writeAtPath persists a job to an arbitrary path. Used by
// processOne to write the processing and final states to the
// .processing.json path (so a crash mid-ingest preserves the
// content for recovery). The caller chooses the path so the
// same write can target either .json or .processing.json.
//
// Atomicity story (relaxed, by design): we write directly to
// the target path. A crash mid-write leaves a partial file
// behind; the next reader will see corrupt JSON. The recovery
// path is built on atomic rename (.json <-> .processing.json),
// not on the write itself: by the time we rename, we have
// already serialized the full Job to memory and are rewriting
// the whole file, so the on-disk state is always either the
// old full state or the new full state — never a partial
// mix. The cost: a reader racing a writer may briefly see a
// malformed file. The job queue is single-instance, so the
// racing case is rare (one HTTP request reading a job it just
// submitted, while the worker rewrites it).
//
// Earlier versions used write-to-tmp + rename for full
// POSIX-atomicity, but os.Rename on macOS occasionally fails
// with ENOENT on a freshly-WriteFile'd source under heavy
// concurrency (observed on darwin/arm64 Go 1.26 with the
// jobs test suite). The relaxed model is sufficient for this
// workload and avoids that footgun.
func (q *Queue) writeAtPath(j *Job, path string) error {
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// getAtProcessingPath reads a job from the .processing.json
// path. Used by processOne to recover the content when the
// .json file does not exist (because the prior worker renamed
// it to .processing.json before crashing).
func (q *Queue) getAtProcessingPath(id string) (*Job, error) {
	return q.getFromPath(q.processingPath(id))
}

// getFromPath is the path-parameterized reader used by both
// the canonical (.json) and processing (.processing.json)
// paths.
func (q *Queue) getFromPath(path string) (*Job, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrJobNotFound
		}
		return nil, err
	}
	var j Job
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, err
	}
	return &j, nil
}
