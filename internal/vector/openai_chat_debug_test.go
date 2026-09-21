package vector

import (
	"context"
	"strings"
	"testing"

	"github.com/ragabast/internal/reqid"
	"github.com/stretchr/testify/assert"
)

// TestChatDebugLine_BasicFormat pins the no-correlation-id
// contract: a chat-debug line without a request ID is the same
// shape operators were grepping for before issue #11 landed.
// No silent format drift for the common case.
func TestChatDebugLine_BasicFormat(t *testing.T) {
	line := chatDebugLine(context.Background(), "request", "hello world")
	assert.Equal(t, "[chat-debug] request: hello world", line,
		"no request ID — format unchanged")
}

// TestChatDebugLine_IncludesRequestIDWhenPresent verifies that
// ctx with a request ID is reflected in the log line so an
// operator can correlate the chat call with the originating
// HTTP request.
func TestChatDebugLine_IncludesRequestIDWhenPresent(t *testing.T) {
	ctx := reqid.WithID(context.Background(), "trace-abc-123")
	line := chatDebugLine(ctx, "request", "hello world")
	assert.Equal(t, "[chat-debug] request request_id=trace-abc-123: hello world", line)
}

// TestChatDebugLine_OmitsRequestIDWhenEmpty pins that an empty
// request ID produces the same output as a missing one — no
// spurious "request_id=" with an empty value.
func TestChatDebugLine_OmitsRequestIDWhenEmpty(t *testing.T) {
	// reqid.WithID with "" is a no-op so the resulting ctx
	// behaves like an absent ID; this test guards against a
	// future change that would emit "request_id=" with an
	// empty value.
	ctx := reqid.WithID(context.Background(), "")
	line := chatDebugLine(ctx, "request", "hello world")
	assert.Equal(t, "[chat-debug] request: hello world", line)
}

// TestChatDebugLine_TruncatesLongBodies guards the body size cap
// so a multi-KiB prompt cannot pin a log line.
func TestChatDebugLine_TruncatesLongBodies(t *testing.T) {
	body := strings.Repeat("a", chatDebugLogMaxBytes+100)
	line := chatDebugLine(context.Background(), "request", body)
	assert.Contains(t, line, "...[truncated]",
		"oversized body must be truncated with the marker")
	assert.LessOrEqual(t, len(line), chatDebugLogMaxBytes+len("[chat-debug] request: ...[truncated]"),
		"line length must respect the size cap")
}

// TestChatDebugLine_HandlesEmptyBody pins that the function is
// safe to call with an empty body — useful for the response
// path when the upstream returns 0 bytes (defensive logging).
func TestChatDebugLine_HandlesEmptyBody(t *testing.T) {
	line := chatDebugLine(context.Background(), "response", "")
	assert.Equal(t, "[chat-debug] response: ", line)
}

// TestChatDebugLine_DistinguishesLabel covers that the label
// (e.g. "request" vs "response") appears in the output. Easy
// regression to miss without an explicit test.
func TestChatDebugLine_DistinguishesLabel(t *testing.T) {
	req := chatDebugLine(context.Background(), "request", "x")
	resp := chatDebugLine(context.Background(), "response", "x")
	assert.Contains(t, req, "request:")
	assert.Contains(t, resp, "response:")
	assert.NotEqual(t, req, resp)
}
