package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/service"
)

// TestWithService_ConfigErrorIsReturned pins the shared preamble
// every service-backed CLI command runs through (query, search,
// ingest, chat, mcp serve — six commands). It had no coverage at
// all.
//
// The contract: a config that cannot be loaded fails the command
// BEFORE the callback runs. The callback touches the live vector
// store and the LLM client, so running it with a broken config
// would fail in a much more confusing place.
func TestWithService_ConfigErrorIsReturned(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	missing := filepath.Join(tmp, "no-such-config.yml")

	called := false
	err := withService(ConfigOpts{Config: missing}, func(context.Context, *config.Config, *service.Service) error {
		called = true
		return nil
	})

	require.Error(t, err, "a config that cannot be loaded must fail the command")
	require.False(t, called, "the callback must not run when the config fails to load")
}

// TestWithService_CallbackErrorIsReturned pins the other half of
// the contract: the callback's own error propagates unchanged, so
// a command can report why it failed.
func TestWithService_CallbackErrorIsReturned(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	cfgPath := filepath.Join(tmp, "config.yml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(
		"vectordb:\n  persistence_dir: "+filepath.Join(tmp, "data", "vectors")+"\n"+
			"  collection_name: test\n"+
			"  embedding_dimension: 768\n"+
			"ollama:\n  base_url: http://127.0.0.1:8000/v1\n"+
			"  embedding_model: x\n"+
			"  chat_base_url: http://127.0.0.1:8000/v1\n"+
			"  chat_model: x\n"+
			"  timeout: 30s\n"+
			"server:\n  address: 0.0.0.0\n  port: 8080\n"+
			"processing:\n  max_chunk_size: 2000\n  min_chunk_size: 300\n  chunk_overlap: 150\n"+
			"paths:\n  data_dir: "+filepath.Join(tmp, "data")+"\n"+
			"  templates_dir: \"\"\n",
	), 0o644))

	sentinel := os.ErrInvalid
	err := withService(ConfigOpts{Config: cfgPath}, func(context.Context, *config.Config, *service.Service) error {
		return sentinel
	})

	require.ErrorIs(t, err, sentinel,
		"the callback's error must reach the caller unchanged")
}
