package service

import (
	"context"
	"testing"

	"github.com/ragabast/internal/config"
	"github.com/stretchr/testify/require"
)

// TestService_GetStats_ChatModelKeyIsStable pins the keys the
// service returns from GetStats. Callers (notably cmd/root.go's
// StatusCmd) read specific keys by name; renaming a key without
// updating the reader would silently print empty values.
//
// This test was added to fix the regression where StatusCmd
// looked up stats["generation_model"] but GetStats produced
// stats["chat_model"]. Keep the names in sync.
func TestService_GetStats_ChatModelKeyIsStable(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Ollama.ChatModel = "expected-chat-model"
	cfg.Ollama.EmbeddingModel = "expected-embedding-model"
	cfg.VectorDB.CollectionName = "expected-collection"

	svc, err := NewService(cfg)
	if err != nil {
		// NewService opens a real Badger DB and constructs an LLM
		// client; both can fail in test environments. We don't want
		// to require a live DB for a stats shape test, so skip
		// instead of failing.
		t.Skipf("NewService unavailable in this environment: %v", err)
	}

	stats, err := svc.GetStats(context.Background())
	require.NoError(t, err)

	require.Equal(t, "expected-chat-model", stats["chat_model"],
		"chat_model key is what cmd/root.go::StatusCmd reads")
	require.Equal(t, "expected-embedding-model", stats["embedding_model"])
	require.Equal(t, "expected-collection", stats["collection_name"])
}
