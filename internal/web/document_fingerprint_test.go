package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/service"
	"github.com/stretchr/testify/require"
)

// TestGetDocumentFingerprint_NotIngestedReturns404 pins the
// canonical "no document with this UID" response: HTTP 404
// with a Huma-shaped detail body. The handler must NOT
// return 200 with an empty fingerprint — that would let an
// ingest pipeline accidentally skip sending a document that
// has never been seen.
func TestGetDocumentFingerprint_NotIngestedReturns404(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeService{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/documents/never-ingested/fingerprint", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code,
		"GET on an unstored UID must return 404, not 200 with empty body")
	require.Contains(t, w.Body.String(), "never-ingested",
		"404 body must name the UID so the operator can see what they asked for")
}

// TestGetDocumentFingerprint_ReturnsStoredFingerprint pins the
// happy path: after a doc is "ingested" (fakeService returns
// exists=true), the handler returns 200 with the stored
// fingerprint + ingested_at. This is the response ingest
// pipelines read to decide skip vs. re-ingest.
func TestGetDocumentFingerprint_ReturnsStoredFingerprint(t *testing.T) {
	want := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	svc := &fakeService{
		fingerprintInfo: service.DocumentFingerprintInfo{
			Fingerprint: "sha256:abc123",
			IngestedAt:  want,
		},
		fingerprintExists: true,
	}
	cfg := config.DefaultConfig()
	s := NewServer(cfg, svc)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/documents/adr-001/fingerprint", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body, _ := io.ReadAll(w.Body)
	var got documentFingerprintResponseBody
	require.NoError(t, json.Unmarshal(body, &got),
		"response body must be valid JSON matching documentFingerprintResponseBody")
	require.Equal(t, "adr-001", got.UID)
	require.Equal(t, "sha256:abc123", got.Fingerprint)
	require.True(t, got.IngestedAt.Equal(want),
		"ingested_at must round-trip the stored timestamp; got %s want %s",
		got.IngestedAt.Format(time.RFC3339), want.Format(time.RFC3339))
}

// TestGetDocumentFingerprint_RequiresUID pins the input-
// validation contract: a request with an empty UID path
// segment must NOT return 200 with an empty result. The exact
// failure code depends on the routing layer — Huma + chi
// emit 422 Unprocessable Entity when the path doesn't bind
// (the most common case in the current stack). We accept any
// 4xx that isn't 200.
func TestGetDocumentFingerprint_RequiresUID(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeService{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/documents//fingerprint", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.NotEqual(t, http.StatusOK, w.Code,
		"missing UID must produce a 4xx, got %d", w.Code)
	require.True(t, w.Code >= 400 && w.Code < 500,
		"missing UID must be a client error (4xx), got %d", w.Code)
}

// TestGetDocumentFingerprint_ContentTypeJSON pins the wire
// shape: the response must be JSON (not a Huma-default
// problem+json error envelope, since this is the happy path).
// Without this assertion, a future "let's optimize and skip
// JSON encoding for short responses" change could break
// client integrations silently.
func TestGetDocumentFingerprint_ContentTypeJSON(t *testing.T) {
	svc := &fakeService{
		fingerprintInfo: service.DocumentFingerprintInfo{
			Fingerprint: "sha256:abc",
			IngestedAt:  time.Now(),
		},
		fingerprintExists: true,
	}
	cfg := config.DefaultConfig()
	s := NewServer(cfg, svc)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/documents/adr-001/fingerprint", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Header().Get("Content-Type"), "application/json",
		"200 responses must be JSON so client parsers work without sniffing")
}

// TestGetDocumentFingerprint_EmptyFingerprintStillReturns200
// pins the "doc was ingested but stored fingerprint is empty"
// edge case: the handler returns 200 with whatever fingerprint
// the vector layer returned. Operators hitting this likely
// have a pre-fingerprint-version ingest in their store — the
// fix is to re-ingest, but we don't fail the lookup.
//
// Empty fingerprint + exists=true is a state that shouldn't
// arise in normal operation (the parser always populates a
// fingerprint at ingest), so we only assert the handler
// doesn't panic and the response shape is valid.
func TestGetDocumentFingerprint_EmptyFingerprintStillReturns200(t *testing.T) {
	svc := &fakeService{
		fingerprintInfo: service.DocumentFingerprintInfo{
			Fingerprint: "", // defensive: pre-fingerprint legacy row
			IngestedAt:  time.Now(),
		},
		fingerprintExists: true,
	}
	cfg := config.DefaultConfig()
	s := NewServer(cfg, svc)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/documents/legacy-uid/fingerprint", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code,
		"ingested docs with empty stored fingerprints still return 200; clients compare and re-ingest if needed")
	body, _ := io.ReadAll(w.Body)
	var got documentFingerprintResponseBody
	require.NoError(t, json.Unmarshal(body, &got))
	require.Equal(t, "legacy-uid", got.UID)
	require.Empty(t, got.Fingerprint)
}

// Reference the models import so it stays in the test file even
// if all tests stop referencing it directly. The full type
// surface from internal/models is implicit through
// service.DocumentFingerprintInfo.
var _ = models.ErrInvalidInput
