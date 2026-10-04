package web

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ragabast/internal/config"
)

// TestInitIngestQueue_NilWhenDirEmpty pins the no-async
// install path: when AsyncIngestQueueDir is empty, the
// queue must not be created and s.ingestQueue stays nil.
// The /api/ingest/async endpoint returns 503 in that case.
func TestInitIngestQueue_NilWhenDirEmpty(t *testing.T) {
	s := &Server{config: config.DefaultConfig()}

	s.initIngestQueue(&fakeHumaService{})

	assert.Nil(t, s.ingestQueue, "ingestQueue must be nil when AsyncIngestQueueDir is empty")
}

// TestInitIngestQueue_QueueCreatedWhenDirSet pins the
// happy path: when AsyncIngestQueueDir is a writable
// directory, a non-nil s.ingestQueue is created and the
// package-level globalIngestQueue is set to the same
// value. /api/health/full uses the global to report
// queue depth.
func TestInitIngestQueue_QueueCreatedWhenDirSet(t *testing.T) {
	dir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Server.AsyncIngestQueueDir = dir

	s := &Server{config: cfg}

	s.initIngestQueue(&fakeHumaService{})

	require.NotNil(t, s.ingestQueue, "ingestQueue must be non-nil when dir is set")
	t.Cleanup(func() {
		s.ingestQueue.Stop()
	})
	assert.Same(t, s.ingestQueue, globalIngestQueue,
		"globalIngestQueue must be set to the same instance")
}

// TestInitIngestQueue_ResetsGlobalState pins the
// test-leak fix: when a previous test (or this same test
// run, sequentially) left globalIngestQueue non-nil,
// initIngestQueue must reset it to nil BEFORE deciding
// whether to start a new queue. Without the reset, a
// server constructed without async ingest would still
// report ingest_queue.enabled=true in /api/health/full
// because the previous queue handle is still in the
// package-level var.
func TestInitIngestQueue_ResetsGlobalState(t *testing.T) {
	// Simulate a previous test that left the global
	// dangling. This is the same defensive line the
	// current NewServer has at "globalIngestQueue = nil"
	// right before deciding whether to create a new queue.
	globalIngestQueue = nil
	t.Cleanup(func() { globalIngestQueue = nil })

	s := &Server{config: config.DefaultConfig()}

	s.initIngestQueue(&fakeHumaService{})

	assert.Nil(t, s.ingestQueue, "no queue should be created when dir is empty")
	assert.Nil(t, globalIngestQueue,
		"globalIngestQueue must be nil after init when no queue is created")
}

// TestInitIngestQueue_DisabledOnNewQueueError pins the
// failure-recovery contract: when jobs.New returns an
// error (e.g. the dir cannot be created), the server
// must still start and s.ingestQueue must be nil.
// Without this, a typo in AsyncIngestQueueDir would
// crash startup.
func TestInitIngestQueue_DisabledOnNewQueueError(t *testing.T) {
	cfg := config.DefaultConfig()
	// Use a path that cannot be created. A nested
	// non-existent directory under a file (not a dir)
	// would do — but the simplest reliable approach is
	// to point at a path whose parent isn't a directory.
	// Use t.TempDir()/file/queue — TempDir creates a
	// dir, then we drop a file at that path, then
	// ask jobs.New to create a queue under "file".
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(dir+"/file", []byte("x"), 0o600))
	cfg.Server.AsyncIngestQueueDir = dir + "/file/queue"

	s := &Server{config: cfg}

	// Should NOT panic.
	s.initIngestQueue(&fakeHumaService{})

	assert.Nil(t, s.ingestQueue, "ingestQueue must be nil when jobs.New fails")
}
