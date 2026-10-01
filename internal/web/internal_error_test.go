package web

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInternalError_NilRequestDoesNotPanic pins the
// regression catcher for the v0.11.0 bug "second chat
// message does nothing." The trigger:
//
//   - handleChatMessage calls renderTemplate with r=nil
//   - template Execute fails (in the buggy case, the LLM
//     response exceeded server.WriteTimeout and the
//     stream was truncated)
//   - renderTemplate calls internalError(w, nil, ...)
//   - internalError did log.Printf("server: %s %s: %v",
//     op, r.URL.Path, err)
//   - r.URL.Path deref of a nil *http.Request panicked
//   - the panic crashed the request goroutine mid-write,
//     leaving the response body in a malformed state; the
//     browser received HTTP 200 with a truncated body
//     and htmx silently failed to swap it in.
//
// The fix: guard the path lookup behind r != nil. This
// test exercises the nil-r path directly to make sure a
// regression here breaks CI rather than the user's chat.
func TestInternalError_NilRequestDoesNotPanic(t *testing.T) {
	w := httptest.NewRecorder()

	require.NotPanics(t, func() {
		internalError(w, nil, "test op", errors.New("simulated template execute failure"))
	}, "internalError(w, nil, ...) must not panic when the request is nil")

	assert.Equal(t, http.StatusInternalServerError, w.Code,
		"the response must be 500 — the operator gets a generic error, the underlying cause goes to the server log")
	assert.Contains(t, w.Body.String(), "Internal server error",
		"the body must carry the generic 500 message so the operator doesn't see internal details")
}

// TestInternalError_NilRequestWithNilErrorDoesNothing
// covers the edge case where someone calls internalError
// with both r=nil and err=nil. The function should be a
// no-op (no log line, no response write) — the caller is
// saying "something went wrong, give up cleanly", which
// without an underlying error is meaningless but should
// not panic.
func TestInternalError_NilRequestWithNilErrorDoesNothing(t *testing.T) {
	w := httptest.NewRecorder()

	require.NotPanics(t, func() {
		internalError(w, nil, "test op", nil)
	}, "internalError(w, nil, ...) must not panic")

	// With err=nil the function should not even write the
	// 500 — the contract is "log and write 500 if err !=
	// nil". A nil err means there's nothing to surface.
	assert.Equal(t, http.StatusOK, w.Code,
		"with err=nil internalError must not write a 500; the function is a no-op")
}

// TestInternalError_WithRequestUsesPath covers the happy
// path: when r is non-nil, the path is included in the log
// line so operators can diagnose which endpoint failed.
// We can't easily assert on log output here, but we can
// assert the function returns the right response code
// without panicking.
func TestInternalError_WithRequestUsesPath(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/some/path", nil)

	require.NotPanics(t, func() {
		internalError(w, r, "test op", errors.New("boom"))
	}, "internalError with a valid request must not panic")

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
