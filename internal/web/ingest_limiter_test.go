package web

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestIngestLimiter_NewIngestLimiter_NormalizesZeroAndNegativeInputs
// pins the constructor's defensive defaults: a zero or negative
// concurrency or retry-after must not produce a broken limiter
// that deadlocks or returns a useless Retry-After header.
func TestIngestLimiter_NewIngestLimiter_NormalizesZeroAndNegativeInputs(t *testing.T) {
	l := NewIngestLimiter(0, 0)
	require.NotNil(t, l)
	require.Equal(t, 1, l.RetryAfterSeconds(), "zero retryAfter should default to 1s")

	l = NewIngestLimiter(-5, -10*time.Second)
	require.NotNil(t, l)
	require.Equal(t, 1, l.RetryAfterSeconds())
}

// TestIngestLimiter_TryAcquireRelease pins the semaphore semantics:
// exactly maxConcurrent acquisitions succeed before saturation,
// and each Release frees one slot.
func TestIngestLimiter_TryAcquireRelease(t *testing.T) {
	l := NewIngestLimiter(2, time.Second)

	require.True(t, l.TryAcquire(), "first acquire should succeed")
	require.True(t, l.TryAcquire(), "second acquire should succeed")
	require.False(t, l.TryAcquire(), "third acquire must fail when semaphore is full")

	l.Release()
	require.True(t, l.TryAcquire(), "release should free a slot for the next acquire")
}

// TestIngestLimiter_TryAcquire_NilReceiverIsAlwaysAllowed guards
// the nil-safe code path. A nil *IngestLimiter (the value passed
// when no limiter is configured) must always admit a request so
// that the ingest endpoints behave the same with or without a
// limiter present.
func TestIngestLimiter_TryAcquire_NilReceiverIsAlwaysAllowed(t *testing.T) {
	var l *IngestLimiter
	require.True(t, l.TryAcquire(), "nil limiter must always admit")
	require.NotPanics(t, func() { l.Release() }, "nil limiter Release must be a no-op")
}

// TestIngestLimiter_RetryAfterHeader_SetsRetryAfter pins that
// RetryAfterHeader produces the expected http.Header shape so the
// Huma error path can rely on it.
func TestIngestLimiter_RetryAfterHeader_SetsRetryAfter(t *testing.T) {
	l := NewIngestLimiter(1, 5*time.Second)
	h := l.RetryAfterHeader()
	require.Equal(t, "5", h.Get("Retry-After"))
}
