package config

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConfig_ValidateRejectsWriteTimeoutShorterThanLLM pins
// the contract that caught the v0.11.0 regression: when the
// HTTP WriteTimeout is shorter than the configured LLM
// per-call timeout, the server closes the response stream
// mid-stream once the LLM call exceeds the WriteTimeout. The
// operator sees the first chat message render and the
// second silently fail to swap — the response body was
// truncated before htmx could parse it.
//
// Validate must reject this configuration at startup so
// the failure shows up in `ragabast serve` rather than as a
// confusing UX bug in production.
func TestConfig_ValidateRejectsWriteTimeoutShorterThanLLM(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Server.WriteTimeout = 10 * time.Second
	cfg.Ollama.Timeout = 60 * time.Second

	err := cfg.Validate()
	require.Error(t, err, "Validate must reject a WriteTimeout shorter than the LLM timeout")
	assert.Contains(t, err.Error(), "write_timeout",
		"the error message must name the offending field so the operator knows what to fix")
	// Go's time.Duration.String formats 60*time.Second as
	// "1m0s" — the error message uses the same formatter,
	// so the test pins the formatted output rather than the
	// raw "60s" the operator typed.
	assert.Contains(t, err.Error(), "1m0s",
		"the error must surface the configured LLM timeout so the operator can size WriteTimeout correctly")
}

// TestConfig_ValidateAcceptsWriteTimeoutAtBoundary pins the
// "just enough" boundary: WriteTimeout == LLM timeout + 5s
// buffer is accepted. One second below the boundary is
// rejected.
func TestConfig_ValidateAcceptsWriteTimeoutAtBoundary(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Ollama.Timeout = 30 * time.Second
	cfg.Server.WriteTimeout = 35 * time.Second // exactly the boundary (30s + 5s buffer)

	assert.NoError(t, cfg.Validate(),
		"WriteTimeout == LLM timeout + 5s buffer should be accepted")
}

// TestConfig_ValidateAcceptsWriteTimeoutAboveBoundary pins
// the typical case: a generous WriteTimeout (90s) for a
// default 30s LLM timeout. This is the safe configuration
// the operator should land on.
func TestConfig_ValidateAcceptsWriteTimeoutAboveBoundary(t *testing.T) {
	cfg := DefaultConfig()
	// DefaultConfig() now sets WriteTimeout=60s and
	// Ollama.Timeout=30s; default Validate should pass.
	assert.NoError(t, cfg.Validate(),
		"DefaultConfig() must pass Validate; if this fails the default WriteTimeout no longer covers the default LLM timeout")
}

// TestConfig_ValidateChecksLongerOfTwoProviders covers the
// edge case where the operator configures both providers
// (Ollama + EmbeddingGemma) with different timeouts. The
// validation must use the longer of the two, because chat
// responses stream from Ollama.ChatModel which is what
// determines the slowest per-call path; if EmbeddingGemma
// is the slower of the two and Ollama.WriteTimeout was
// tuned to the Ollama timeout, a chat request would still
// hit the EmbeddingGemma path during ingestion.
//
// The right answer is "WriteTimeout must cover the slowest
// path anywhere in the request lifecycle", which is the
// max of both.
func TestConfig_ValidateChecksLongerOfTwoProviders(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Ollama.Timeout = 30 * time.Second
	cfg.EmbeddingGemma.Timeout = 120 * time.Second
	cfg.Server.WriteTimeout = 60 * time.Second // comfortably > Ollama, < EmbeddingGemma

	err := cfg.Validate()
	require.Error(t, err,
		"WriteTimeout must cover the slower of the two configured LLM timeouts")
	assert.True(t, strings.Contains(err.Error(), "120s") || strings.Contains(err.Error(), "2m0s"),
		"the error must name the longer provider's timeout (120s / 2m0s); got %q", err.Error())
}
