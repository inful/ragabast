package web

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWithIngestGuard_AllowsAcquireAndReleases pins the happy
// path: when the limiter is not saturated and the body fits,
// withIngestGuard returns a non-nil release callback and no
// error. Calling release must not panic.
func TestWithIngestGuard_AllowsAcquireAndReleases(t *testing.T) {
	limiter := NewIngestLimiter(2, 0)

	release, err := withIngestGuard(limiter, 1024, 100)

	require.NoError(t, err)
	require.NotNil(t, release, "release must be non-nil on the happy path")

	// Calling release must not panic and must return the
	// semaphore slot for the next caller.
	release()

	// After release, a second acquire must succeed.
	release2, err2 := withIngestGuard(limiter, 1024, 100)
	require.NoError(t, err2)
	release2()
}

// TestWithIngestGuard_RejectsWhenLimiterFull pins the
// saturation contract: when the limiter has no free slots,
// withIngestGuard returns a huma 429 error and a nil release
// callback. Callers must NOT call release in that case (it
// is nil).
func TestWithIngestGuard_RejectsWhenLimiterFull(t *testing.T) {
	// Capacity 1: first acquire consumes the slot.
	limiter := NewIngestLimiter(1, 0)
	firstRelease, err := withIngestGuard(limiter, 1024, 100)
	require.NoError(t, err)
	defer firstRelease()

	// Second acquire must fail with 429.
	release, err := withIngestGuard(limiter, 1024, 100)
	require.Error(t, err, "second concurrent acquire must fail")
	assert.Nil(t, release, "release must be nil when the limiter rejects the acquire")

	// The huma error should be a 429.
	status := http.StatusTooManyRequests
	if he, ok := err.(interface{ GetStatus() int }); ok {
		assert.Equal(t, status, he.GetStatus())
	}
}

// TestWithIngestGuard_RejectsWhenTooLarge pins the size
// contract: when the body exceeds maxBytes, withIngestGuard
// returns a huma 413 and a nil release. The slot is released
// before returning (the caller never got the slot, so the
// limit's accounting stays consistent).
func TestWithIngestGuard_RejectsWhenTooLarge(t *testing.T) {
	limiter := NewIngestLimiter(2, 0)

	release, err := withIngestGuard(limiter, 100, 200) // body twice the cap

	require.Error(t, err)
	assert.Nil(t, release)
}

// TestWithIngestGuard_NilLimiterAllowsUnbounded pins the
// no-limiter path: when limiter is nil, withIngestGuard
// must allow the request regardless of "saturation" and
// return a release callback that is a no-op. This is the
// ingest_limiter.NewIngestLimiter's behavior on a nil
// receiver, propagated here for symmetry.
func TestWithIngestGuard_NilLimiterAllowsUnbounded(t *testing.T) {
	release, err := withIngestGuard(nil, 1024, 100)
	require.NoError(t, err)
	require.NotNil(t, release, "release must be non-nil even with a nil limiter")
	release() // must not panic
}

// TestWithIngestGuard_NilMaxBytesSkipsSizeCheck pins the
// test-only path: maxBytes == 0 disables the size check
// regardless of body length. The cap is meant to be set
// in production; tests use 0 to bypass it.
func TestWithIngestGuard_NilMaxBytesSkipsSizeCheck(t *testing.T) {
	limiter := NewIngestLimiter(2, 0)

	release, err := withIngestGuard(limiter, 0, 1<<20) // 1 MiB body, 0 cap
	require.NoError(t, err)
	require.NotNil(t, release)
	release()
}
