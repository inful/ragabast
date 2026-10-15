package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// RagabastConfig holds ragabast-specific presentation / linking
// configuration that doesn't belong under one of the existing
// provider- or storage-specific sections.
type RagabastConfig struct {
	// DocbuilderBaseURL is the root URL of the docbuilder
	// instance that renders the ingested documents. When set,
	// every search result and document info gets a synthetic
	// `docbuilder_url` field computed as
	// `<DocbuilderBaseURL>/<uid>`. Empty (the default) keeps
	// behavior unchanged — only the doc's own frontmatter
	// `urls:` surface as links.
	DocbuilderBaseURL string `env:"RAGABAST_DOCBUILDER_BASE_URL" yaml:"docbuilder_base_url,omitempty"`

	// LogChatRequests, when true, makes the chat-completions
	// client emit the full request body and full response body
	// under the [chat-debug] log prefix on every chat call.
	// Operators use this to verify the system prompt actually
	// reaches the model and to inspect what the model
	// returned, including the parts that ragabast's
	// post-processors strip from the user-visible reply.
	//
	// Default false: chat prompts can be large (the context
	// block routinely runs thousands of characters) and may
	// include sensitive operator content. The bearer token is
	// never logged — only the request and response bodies.
	// Bodies larger than 8 KiB are truncated with a
	// "...[truncated]" marker to keep the log scannable.
	LogChatRequests bool `env:"RAGABAST_LOG_CHAT_REQUESTS" yaml:"log_chat_requests,omitempty"`

	// QueryCacheSize bounds the LRU query cache (issue #13).
	// Every /api/search and /api/hybrid call hits the cache
	// before running the embedding + vector DB pipeline;
	// setting this > 0 turns the cache on. Default 256 — a
	// reasonable upper bound for a single-operator
	// installation. Set to 0 to disable the cache entirely
	// (every query runs end-to-end; useful for benchmarking
	// or for operators chasing a suspected stale-result bug).
	QueryCacheSize int `env:"RAGABAST_QUERY_CACHE_SIZE" yaml:"query_cache_size,omitempty"`

	// QueryCacheTTL bounds entry staleness regardless of
	// invalidation. Even with the IngestDocument /
	// DeleteDocument hooks clearing the cache, a paranoid
	// operator can set this so stale results can never
	// outlive a fixed window. Default 0 = no TTL (entries
	// live until evicted by capacity). Format: a Go
	// duration string (e.g. "5m", "1h").
	QueryCacheTTL time.Duration `env:"RAGABAST_QUERY_CACHE_TTL" yaml:"query_cache_ttl,omitempty"`

	// ChatSessionMaxTurns caps the number of user/
	// assistant exchanges persisted per chat session
	// (issue #22). Oldest turns are dropped FIFO when
	// the session exceeds this — operators get enough
	// context for follow-up questions without unbounded
	// memory growth. Default 20 (≈40 messages).
	ChatSessionMaxTurns int `env:"RAGABAST_CHAT_SESSION_MAX_TURNS" yaml:"chat_session_max_turns,omitempty"`
}

// validateAndTrimDocbuilderBaseURL validates the configured
// docbuilder base URL AND persists the trimmed form back
// onto the config. The mutation is intentional: the only
// legitimate whitespace around a URL is from copy/paste,
// and silently passing " http://x.com/ " through would
// produce " http://x.com/_uid/abc/" links in the chat
// UI — broken and visibly wrong.
//
// The scheme allow-list (http, https) protects against a
// more dangerous silent failure than a typo: a javascript:
// or file:// scheme would render the permalink as either
// an XSS vector or a path disclosure.
func validateAndTrimDocbuilderBaseURL(c *Config) error {
	raw := c.Ragabast.DocbuilderBaseURL
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		c.Ragabast.DocbuilderBaseURL = ""
		return nil
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return fmt.Errorf("ragabast.docbuilder_base_url %q is not a valid URL: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("ragabast.docbuilder_base_url %q must use http or https scheme (got %q)", raw, u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("ragabast.docbuilder_base_url %q must include a host", raw)
	}
	c.Ragabast.DocbuilderBaseURL = trimmed
	return nil
}
