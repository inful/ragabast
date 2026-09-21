package reqid

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestFromContext_EmptyByDefault pins the no-middleware contract:
// callers that read the ID without WithID being called get ""
// rather than a panic or a fabricated value.
func TestFromContext_EmptyByDefault(t *testing.T) {
	assert.Empty(t, FromContext(context.Background()))
}

// TestFromContext_ReturnsValueSet verifies the round-trip.
func TestFromContext_ReturnsValueSet(t *testing.T) {
	ctx := WithID(context.Background(), "abc-123")
	assert.Equal(t, "abc-123", FromContext(ctx))
}

// TestFromContext_NestedValues pins that nested WithID calls
// return the inner value (the standard context.WithValue
// semantics). Useful for handlers that wrap the request context
// further — e.g. an async-ingest path that builds a new context
// per job.
func TestFromContext_NestedValues(t *testing.T) {
	outer := WithID(context.Background(), "outer-id")
	inner := WithID(outer, "inner-id")
	assert.Equal(t, "inner-id", FromContext(inner),
		"nested WithID returns the most-recently-set value")
	assert.Equal(t, "outer-id", FromContext(outer),
		"outer's ID is preserved when inner only reads")
}

// TestWithID_EmptyIsNoop documents that an empty ID doesn't
// allocate a new context — useful when the caller doesn't know
// whether an ID is present and wants to call WithID defensively.
func TestWithID_EmptyIsNoop(t *testing.T) {
	ctx := WithID(context.Background(), "")
	assert.Empty(t, FromContext(ctx))
}
