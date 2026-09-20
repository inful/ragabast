package service

import (
	"context"

	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/vector"
)

// llmChatClient is the slice of *vector.OpenAILLMClient that the
// query pipeline depends on. It exists so that the LLM client can
// be constructed once at Service init and reused across requests,
// and so that future tests can substitute a stub without standing
// up a real HTTP chat-completions client.
type llmChatClient interface {
	Chat(ctx context.Context, messages []vector.OpenAIMessage, options map[string]any) (string, error)
	ChatWithSystem(ctx context.Context, systemPrompt, userPrompt string, options map[string]any) (string, error)
}

// newLLMChatClient is the single place where the chat-completions
// client is built. QueryDebugWithOptions MUST use the client held
// on the Service (constructed via this function in NewService)
// rather than rebuilding a client per request.
func newLLMChatClient(cfg *config.Config) llmChatClient {
	return vector.NewOpenAILLMClientWithOptions(
		cfg.Ollama.ChatBaseURL,
		cfg.Ollama.ChatModel,
		cfg.Ollama.EffectiveChatAPIKey(),
		cfg.Ollama.Timeout,
		cfg.Ragabast.LogChatRequests,
	)
}
