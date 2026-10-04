package web

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// withIngestGuard is the shared preamble for the three ingest
// endpoints (POST /api/ingest, /api/ingest/raw, /api/ingest/file)
// and POST /api/ingest/gitlab/issue. It bundles the
// acquire-release dance for the per-server ingest limiter and
// the per-document size check so each handler is just the
// actual ingest call + response shape.
//
// Behavior:
//
//   - limiter is nil → no rate limiting; release is a no-op.
//   - bodyLen > maxBytes → huma 413 + nil release.
//   - limiter.TryAcquire() returns false → huma 429 + Retry-After
//     header + nil release.
//
// On success, release() returns the limiter slot. Callers
// MUST defer release() when err == nil. When err != nil the
// slot is either not acquired (429 path) or the size check
// short-circuited before the acquire — release is nil and
// must NOT be called.
//
// maxBytes <= 0 disables the size check. Production
// callers should always pass a non-zero value via
// cfg.Server.MaxIngestDocumentBytes (default 1 MiB). The 0
// path is for tests only.
//
// Behavior contract (pinned by TestWithIngestGuard_*):
//   - Non-saturated limiter + body within cap → release != nil,
//     err == nil.
//   - Saturated limiter → huma 429, release == nil.
//   - body over cap → huma 413, release == nil.
//   - Nil limiter → no rejection, release is a no-op.
//   - Zero maxBytes → no size check, regardless of body length.
func withIngestGuard(limiter *IngestLimiter, maxBytes, bodyLen int) (release func(), err error) {
	// 1. Acquire a slot in the limiter (or no-op when nil).
	if !acquireIngestSlot(limiter) {
		return nil, huma.ErrorWithHeaders(
			huma.Error429TooManyRequests("ingest busy"),
			limiter.RetryAfterHeader(),
		)
	}

	// 2. Size check. If the body is too large, release the
	//    slot we just acquired (the caller never sees the
	//    release) so the limiter's accounting stays
	//    consistent. A future caller that arrives before
	//    the manual Release will still find a free slot.
	if sizeErr := checkIngestSize(bodyLen, maxBytes); sizeErr != nil {
		if limiter != nil {
			limiter.Release()
		}
		return nil, sizeErr
	}

	// 3. On success, return a release that the caller
	//    defers. Use a closure so we can capture the
	//    limiter (which may be nil — Release is a no-op
	//    on a nil receiver).
	return func() {
		if limiter != nil {
			limiter.Release()
		}
	}, nil
}

// Ensure compile-time usage of http package to avoid
// "imported and not used" if all callers stop referencing
// it directly after migrating to withIngestGuard.
var _ = http.StatusTooManyRequests
