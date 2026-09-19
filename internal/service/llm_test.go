package service

import (
	"testing"

	"github.com/ragabast/internal/config"
	"github.com/stretchr/testify/require"
)

func TestNewLLMChatClient_ReturnsNonNilClient(t *testing.T) {
	cfg := config.DefaultConfig()
	client := newLLMChatClient(cfg)
	require.NotNil(t, client, "newLLMChatClient must return a usable client for the default config")
}
