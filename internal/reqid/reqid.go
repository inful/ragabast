// Package reqid defines the context key and helpers used to
// propagate correlation IDs across HTTP middleware, async job
// submission, worker audit logs, and the LLM chat-debug logger.
//
// Lives in its own leaf package so neither the web, service, nor
// vector layers have to import each other just to read the
// value. Each layer:
//   - writes the ID with WithID(ctx, id)
//   - reads the ID with FromContext(ctx)
// and the underlying context.Context plumbing is identical.
//
// The key is an unexported struct type so it cannot collide with
// string-typed keys an external caller might set; context.Value
// returns the zero value ("") when the value was not set by this
// package.
package reqid

import "context"

// ctxKey is the typed key for storing the request ID. Using a
// struct{} (rather than a string) is the standard Go idiom for
// collision-proof context values.
type ctxKey struct{}

// WithID returns a copy of ctx carrying id as the request ID.
// Empty ids are a no-op: the original ctx is returned so callers
// can chain unconditionally without checking for the empty case.
func WithID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, id)
}

// FromContext returns the request ID stored on ctx, or the empty
// string when no middleware set one. CLI commands and async
// workers run without the HTTP middleware chain; they get a
// defined empty value rather than a panic. Callers that need to
// distinguish "no ID" from "empty ID" should use a typed-pointer
// value instead of a plain string.
func FromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKey{}).(string)
	return v
}
