package vector

import "net/http"

// applyAuth adds an Authorization: Bearer header when apiKey is
// non-empty. Used by both the chat-completions and embeddings
// clients (their bodies differ but the auth header shape is the
// same).
func applyAuth(req *http.Request, apiKey string) {
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
}
