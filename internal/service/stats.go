package service

import "context"

// GetStats returns system statistics.
func (s *Service) GetStats(ctx context.Context) (map[string]any, error) {
	count, err := s.vectorOps.GetStats()
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"total_chunks":    count,
		"embedding_model": s.config.Ollama.EmbeddingModel,
		"chat_model":      s.config.Ollama.ChatModel,
		"collection_name": s.config.VectorDB.CollectionName,
	}, nil
}

// ValidateConnection checks if all services are accessible.
func (s *Service) ValidateConnection(ctx context.Context) error {
	return s.vectorOps.ValidateConnection(ctx)
}

// CheckHealth validates service health.
func (s *Service) CheckHealth(ctx context.Context) (bool, error) {
	if err := s.ValidateConnection(ctx); err != nil {
		return false, err
	}
	return true, nil
}
