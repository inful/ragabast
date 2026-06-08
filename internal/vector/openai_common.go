package vector

import "strings"

// normalizeOpenAIBaseURL returns a base URL with no trailing slash and no
// trailing /v1 segment, so the client can always append /v1/... paths.
//
// Both forms are accepted from callers:
//   - http://host:8000        -> http://host:8000
//   - http://host:8000/       -> http://host:8000
//   - http://host:8000/v1     -> http://host:8000
//   - http://host:8000/v1/    -> http://host:8000
//
// This matches the convention used by omlx, vLLM, LM Studio, llama.cpp,
// llama-stack, and OpenAI's own docs, where the base URL sometimes includes
// /v1 and sometimes does not.
func normalizeOpenAIBaseURL(raw string) string {
	trimmed := strings.TrimRight(raw, "/")
	// Strip a trailing /v1 segment (case-insensitive) so callers can pass
	// either the root URL or the /v1-rooted URL interchangeably.
	if len(trimmed) >= 3 && strings.EqualFold(trimmed[len(trimmed)-3:], "/v1") {
		trimmed = trimmed[:len(trimmed)-3]
	}
	return strings.TrimRight(trimmed, "/")
}
