package config

import (
	"bytes"
	"log"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureLog redirects the package-level default logger's
// output to a buffer for the duration of the test and
// restores it on cleanup. The default logger is a
// stdlib-global shared by every test; the buffer captures
// only what THIS test emits after captureLog returns and
// before cleanup fires.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prevWriter := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() {
		log.SetOutput(prevWriter)
	})
	return &buf
}

// TestEffectiveAuthTokens_DedupsExactMatches pins the existing
// dedup behavior: an operator who lists the same token in both
// AuthToken (singular) and AuthTokens (plural) — a typical
// rotation grace window — gets exactly one entry.
func TestEffectiveAuthTokens_DedupsExactMatches(t *testing.T) {
	cfg := ServerConfig{
		AuthToken: "secret-token",
		AuthTokens: []AuthToken{
			{Label: "alice", Value: "secret-token"},
		},
	}

	eff := cfg.EffectiveAuthTokens()
	require.Len(t, eff, 1, "exact-match dedup must collapse to one entry")
}

// TestEffectiveAuthTokens_CaseDifferenceLogsWarning pins the
// audit finding 12 behavior: tokens are compared case-
// sensitively by matchBearer's subtle.ConstantTimeCompare, so
// an operator who lists "abc" and "ABC" ends up with TWO
// accepted tokens. EffectiveAuthTokens logs a warning so the
// operator can see the duplicate at startup.
func TestEffectiveAuthTokens_CaseDifferenceLogsWarning(t *testing.T) {
	cfg := ServerConfig{
		AuthTokens: []AuthToken{
			{Label: "lowercase", Value: "abc"},
			{Label: "UPPERCASE", Value: "ABC"},
		},
	}
	buf := captureLog(t)

	eff := cfg.EffectiveAuthTokens()
	assert.Len(t, eff, 2,
		"case-different tokens are NOT deduped (wire format is case-sensitive)")
	assert.Contains(t, buf.String(), "case-different auth token",
		"the warning must mention the issue so operators grep log lines")
	assert.True(t,
		strings.Contains(buf.String(), "abc") && strings.Contains(buf.String(), "ABC"),
		"the warning must show both tokens so the operator can locate them in YAML",
	)
}

// TestEffectiveAuthTokens_SameCaseNoWarning pins that the
// dedup map still works correctly: identical-case duplicates
// are collapsed silently (no warning, no double-entry).
func TestEffectiveAuthTokens_SameCaseNoWarning(t *testing.T) {
	cfg := ServerConfig{
		AuthTokens: []AuthToken{
			{Label: "alice", Value: "same-token"},
			{Label: "bob", Value: "same-token"},
		},
	}
	buf := captureLog(t)

	eff := cfg.EffectiveAuthTokens()
	assert.Len(t, eff, 1, "identical-case duplicates collapse silently")
	assert.Empty(t, buf.String(), "no warning for identical-case duplicates (those are intentional via AuthToken/AuthTokens mixing)")
}
