package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDefaultConfig_ChatSessionIdleTTL pins audit finding 10.
//
// The ChatSessionIdleTTL knob was documented with a 24h default,
// but DefaultConfig never set it. The zero value reached
// NewChatSessionStore, where "IdleTTL == 0" means "no GC" — the
// exact opposite of the documented default. So an operator who
// changed nothing got chat sessions that accumulated forever
// instead of being evicted after a day.
func TestDefaultConfig_ChatSessionIdleTTL(t *testing.T) {
	cfg := DefaultConfig()

	assert.Equal(t, 24*time.Hour, cfg.Ragabast.ChatSessionIdleTTL,
		"the default config must enable the chat-session idle TTL GC")
}

// TestDefaultConfig_ChatSessionMaxTurns pins the sibling knob,
// which had the same gap: the documented default was 20 but
// DefaultConfig left it at 0.
func TestDefaultConfig_ChatSessionMaxTurns(t *testing.T) {
	cfg := DefaultConfig()

	assert.Equal(t, 20, cfg.Ragabast.ChatSessionMaxTurns,
		"the default config must set the chat-session turn cap")
}

// TestApplyEnvOverrides_ChatSessionIdleTTL pins that the env
// override for the new knob actually works. The knob's env tag
// existed but ApplyEnvOverrides never read it, so
// RAGABAST_CHAT_SESSION_IDLE_TTL was silently ignored.
func TestApplyEnvOverrides_ChatSessionIdleTTL(t *testing.T) {
	t.Setenv("RAGABAST_CHAT_SESSION_IDLE_TTL", "2h")

	cfg := DefaultConfig()
	cfg.ApplyEnvOverrides()

	assert.Equal(t, 2*time.Hour, cfg.Ragabast.ChatSessionIdleTTL,
		"RAGABAST_CHAT_SESSION_IDLE_TTL must override the default")
}

// TestApplyEnvOverrides_ChatSessionMaxTurns is the same for the
// turn cap.
func TestApplyEnvOverrides_ChatSessionMaxTurns(t *testing.T) {
	t.Setenv("RAGABAST_CHAT_SESSION_MAX_TURNS", "7")

	cfg := DefaultConfig()
	cfg.ApplyEnvOverrides()

	assert.Equal(t, 7, cfg.Ragabast.ChatSessionMaxTurns,
		"RAGABAST_CHAT_SESSION_MAX_TURNS must override the default")
}

// TestConfigExampleDocumentsChatSessionIdleTTL pins audit
// finding 6: the new knob was missing from config.example.yml,
// the file operators copy to bootstrap a deployment. A knob that
// is not in the example file is a knob nobody finds.
func TestConfigExampleDocumentsChatSessionIdleTTL(t *testing.T) {
	example := readRepoFile(t, "config.example.yml")

	assert.Contains(t, example, "chat_session_idle_ttl:",
		"config.example.yml must document the chat_session_idle_ttl knob")
	assert.Contains(t, example, "chat_session_max_turns:",
		"config.example.yml must document the chat_session_max_turns knob")
}

// TestReadmeDocumentsChatSessionIdleTTL pins the same contract
// for the README's configuration tables.
func TestReadmeDocumentsChatSessionIdleTTL(t *testing.T) {
	readme := readRepoFile(t, "README.md")

	assert.Contains(t, readme, "chat_session_idle_ttl",
		"README must document the chat_session_idle_ttl knob")
	assert.Contains(t, readme, "RAGABAST_CHAT_SESSION_IDLE_TTL",
		"README must document the env var for the chat-session idle TTL")
}

// readRepoFile reads a file relative to the repository root
// (the config package's parent directory).
func readRepoFile(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", "..", name)
	data, err := os.ReadFile(path)
	require.NoError(t, err, "must be able to read %s", name)
	return string(data)
}
