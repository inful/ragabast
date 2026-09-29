package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"
	"golang.org/x/oauth2/gitlab"
	"gopkg.in/yaml.v3"
)

// providerNameRegexp pins the URL-slug grammar for OAuthProvider.Name:
// lowercase letters, digits, and hyphens. The name is interpolated
// into /auth/<name>/callback paths so the strict ASCII constraint
// keeps the URL well-formed for any operator's reverse proxy.
var providerNameRegexp = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// reservedProviderNames are slugs the auth layer reserves for
// built-in endpoints (/auth/login, /auth/logout, /auth/me). A
// configured provider with one of these names would shadow a
// built-in route, so Validate rejects them.
var reservedProviderNames = map[string]bool{
	"login":  true,
	"logout": true,
	"me":     true,
}

const (
	// DefaultConfigYML is the default config filename.
	DefaultConfigYML = "config.yml"

	// DefaultConfigYAML is an alternate default config filename.
	DefaultConfigYAML = "config.yaml"
)

// Config represents the application configuration.
type Config struct {
	// Ollama configuration.
	Ollama OllamaConfig `yaml:"ollama"`

	// EmbeddingProvider selects which embeddings client
	// ragabast uses. Empty / "openai" — the historical
	// OpenAI-compat path via ollama.base_url (Ollama,
	// vLLM, LM Studio, llama.cpp --embedding, OpenAI,
	// llama-stack). "embedding_gemma" — the EmbeddingGemma
	// /v2/embed path via embedding_gemma.base_url, which
	// is NOT OpenAI-compatible: the body is {"texts":
	// [...]}, the model and dimensions fields are absent,
	// and there is no Authorization header.
	//
	// The default (empty) preserves the historical
	// behavior so existing config files and operators
	// don't see any change.
	EmbeddingProvider string `env:"EMBEDDING_PROVIDER" yaml:"embedding_provider,omitempty"`

	// EmbeddingGemma holds the embedding-gemma /v2/embed
	// server settings. Only consulted when
	// EmbeddingProvider is "embedding_gemma". Required
	// field is BaseURL.
	EmbeddingGemma EmbeddingGemmaConfig `yaml:"embedding_gemma,omitempty"`

	// Vector database configuration.
	VectorDB VectorDBConfig `yaml:"vectordb"`

	// Server configuration.
	Server ServerConfig `yaml:"server"`

	// Processing configuration.
	Processing ProcessingConfig `yaml:"processing"`

	// Paths configuration.
	Paths PathsConfig `yaml:"paths"`

	// Ragabast holds ragabast-specific presentation knobs.
	Ragabast RagabastConfig `yaml:"ragabast"`

	// Auth configures web-layer user authentication via
	// external OAuth 2.0 / OIDC identity providers. When
	// Auth.Providers is empty, session auth is disabled and
	// the server relies on bearer-token (or open-access)
	// mode — the single-user local install keeps working.
	Auth AuthConfig `yaml:"auth,omitempty"`
}

// AuthConfig holds web-layer authentication settings. When
// Providers is non-empty, the web server runs an OAuth 2.0
// Authorization Code + PKCE flow per provider and accepts
// the resulting session cookie on protected routes. The
// bearer-token path (server.auth_token / server.auth_tokens)
// keeps working in parallel — programmatic API clients do
// not need a browser session to call the JSON API.
type AuthConfig struct {
	// SessionTTL is the maximum age of a session cookie
	// before it expires and the in-memory record is
	// evicted. Default 12h. A sliding-renewal middleware
	// extends the cookie on every authenticated request;
	// the TTL is the idle timeout. Set to 0 to disable
	// session expiry (not recommended for exposed
	// installs).
	SessionTTL time.Duration `env:"AUTH_SESSION_TTL" yaml:"session_ttl,omitempty"`

	// CookieName is the session-cookie name. Default
	// "ragabast_session". Custom names let operators run
	// multiple ragabast instances behind the same host
	// (different ports, different paths) without
	// colliding cookies, or align with an existing
	// cookie-allowlist.
	CookieName string `env:"AUTH_COOKIE_NAME" yaml:"cookie_name,omitempty"`

	// Providers is the list of configured OAuth 2.0 /
	// OIDC identity providers. The order in this slice
	// is the order shown on the login page when more
	// than one provider is configured.
	Providers []OAuthProvider `env:"AUTH_PROVIDERS_JSON" yaml:"providers,omitempty"`
}

// OAuthProvider is one configured OAuth 2.0 / OIDC identity
// provider. The Type field selects the preset endpoint set
// (and, for oidc, signals the handler to run discovery at
// startup). Name is the URL slug under /auth/<name>/callback.
type OAuthProvider struct {
	// Name is the URL slug used in /auth/<name>/login
	// and /auth/<name>/callback. Must match
	// providerNameRegexp (lowercase ASCII letters,
	// digits, hyphens) and must not be one of the
	// reservedProviderNames ("login", "logout", "me").
	Name string `json:"name" yaml:"name"`

	// Type selects the endpoint preset. One of:
	//   "github"  — github.com OAuth app
	//   "gitlab"  — gitlab.com when BaseURL is empty,
	//               or any self-hosted GitLab when
	//               BaseURL is set
	//   "forgejo" — any Forgejo/Gitea instance;
	//               BaseURL is required (e.g.
	//               https://codeberg.org)
	//   "oidc"    — generic OpenID Connect; the
	//               handler runs discovery against
	//               DiscoveryURL at startup
	Type string `json:"type" yaml:"type"`

	// ClientID is the OAuth 2.0 client identifier
	// issued by the provider. Treated as
	// non-sensitive (it's a public identifier).
	ClientID string `json:"client_id" yaml:"client_id"`

	// ClientSecret is the OAuth 2.0 client secret.
	// Treated as sensitive — SaveConfig mode-0600's
	// the file; never log this field.
	ClientSecret string `json:"client_secret" yaml:"client_secret"`

	// BaseURL is the provider's root for self-hosted
	// presets. Used by type=gitlab (overrides the
	// gitlab.com default) and type=forgejo (required).
	// Ignored for type=github and type=oidc.
	BaseURL string `json:"base_url,omitempty" yaml:"base_url,omitempty"`

	// DiscoveryURL is the OIDC issuer URL for
	// type=oidc (e.g. https://keycloak.example.com/realms/main).
	// NewProvider fetches the well-known configuration
	// from <DiscoveryURL>/.well-known/openid-configuration
	// at startup. Required when Type is "oidc"; ignored
	// otherwise.
	DiscoveryURL string `json:"discovery_url,omitempty" yaml:"discovery_url,omitempty"`

	// Scopes override the preset's default scope set.
	// Empty falls back to EffectiveScopes (which picks
	// the right defaults per Type).
	Scopes []string `json:"scopes,omitempty" yaml:"scopes,omitempty"`

	// AllowedUsers is an optional allowlist. Match is
	// on the user's username (provider's `login` or
	// `username` field, depending on the provider) OR
	// email. Empty means any successfully
	// authenticated user from this provider is
	// accepted; non-empty is a strict whitelist.
	AllowedUsers []string `json:"allowed_users,omitempty" yaml:"allowed_users,omitempty"`

	// DefaultRole is the role assigned to
	// authenticated users from this provider. Empty
	// defaults to "user". Role mapping from IdP
	// claims (groups, teams) arrives in a later
	// change.
	DefaultRole string `json:"default_role,omitempty" yaml:"default_role,omitempty"`
}

// Validate reports configuration errors specific to this
// provider. Cross-provider checks (duplicate names) live
// on AuthConfig.Validate so they can be batched into the
// single Validate() error list.
func (p OAuthProvider) Validate() error {
	if p.Name == "" {
		return errors.New("auth.providers[].name is required")
	}
	if !providerNameRegexp.MatchString(p.Name) {
		return fmt.Errorf("auth.providers[].name %q must match %s", p.Name, providerNameRegexp.String())
	}
	switch p.Type {
	case "github", "gitlab", "forgejo", "oidc":
	default:
		return fmt.Errorf("auth.providers[%q].type %q is not one of: github, gitlab, forgejo, oidc", p.Name, p.Type)
	}
	if p.ClientID == "" {
		return fmt.Errorf("auth.providers[%q].client_id is required", p.Name)
	}
	if p.ClientSecret == "" {
		return fmt.Errorf("auth.providers[%q].client_secret is required", p.Name)
	}
	if p.Type == "forgejo" && p.BaseURL == "" {
		return fmt.Errorf("auth.providers[%q].base_url is required for type=forgejo", p.Name)
	}
	if p.Type == "oidc" && p.DiscoveryURL == "" {
		return fmt.Errorf("auth.providers[%q].discovery_url is required for type=oidc", p.Name)
	}

	return nil
}

// Endpoint returns the OAuth 2.0 endpoint set this provider
// speaks. The static presets (github, gitlab, forgejo) all
// have a hard-coded AuthURL+TokenURL pair. OIDC providers
// return an error here because their endpoint is discovered
// at runtime — callers that need it should use RequiresDiscovery
// to dispatch into the go-oidc.NewProvider path instead.
func (p OAuthProvider) Endpoint() (oauth2.Endpoint, error) {
	switch p.Type {
	case "github":
		return github.Endpoint, nil
	case "gitlab":
		if p.BaseURL == "" {
			return gitlab.Endpoint, nil
		}

		base := strings.TrimRight(p.BaseURL, "/")

		return oauth2.Endpoint{
			AuthURL:  base + "/oauth/authorize",
			TokenURL: base + "/oauth/token",
		}, nil
	case "forgejo":
		if p.BaseURL == "" {
			return oauth2.Endpoint{}, fmt.Errorf("auth.providers[%q].base_url is required for type=forgejo", p.Name)
		}

		base := strings.TrimRight(p.BaseURL, "/")

		// Forgejo speaks the GitHub-compatible OAuth shape:
		// /login/oauth/authorize and /login/oauth/access_token.
		return oauth2.Endpoint{
			AuthURL:  base + "/login/oauth/authorize",
			TokenURL: base + "/login/oauth/access_token",
		}, nil
	case "oidc":
		return oauth2.Endpoint{}, fmt.Errorf("auth.providers[%q] type=oidc requires discovery_url and runtime OIDC discovery; use RequiresDiscovery to dispatch", p.Name)
	default:
		return oauth2.Endpoint{}, fmt.Errorf("auth.providers[%q] has unknown type %q", p.Name, p.Type)
	}
}

// RequiresDiscovery reports whether the provider needs the
// go-oidc discovery handshake at startup. Only type=oidc
// requires it — the rest have static endpoint sets and
// skip the discovery round-trip.
func (p OAuthProvider) RequiresDiscovery() bool {
	return p.Type == "oidc"
}

// EffectiveScopes returns the OAuth scopes this provider
// will request. If Scopes is set, it wins (operator
// override). Otherwise the per-type preset default is
// returned.
//
// The defaults are chosen to give the callback handler
// enough to identify the user without asking for more
// than the IdP's basic profile.
//
//   - github:  read:user + user:email — the minimal set
//     that returns a stable user id and a verified email
//   - gitlab:  openid + profile + email — OIDC-style so
//     the gitlab.com IdToken carries the claims
//   - forgejo: read:user + user:email — matches the
//     GitHub-compatible Forgejo OAuth shape
//   - oidc:    openid + profile + email — required by
//     the OIDC spec for ID-token issuance
func (p OAuthProvider) EffectiveScopes() []string {
	if len(p.Scopes) > 0 {
		out := make([]string, len(p.Scopes))
		copy(out, p.Scopes)

		return out
	}
	switch p.Type {
	case "github", "forgejo":
		return []string{"read:user", "user:email"}
	case "gitlab", "oidc":
		return []string{"openid", "profile", "email"}
	default:
		return []string{"openid", "profile", "email"}
	}
}

// RedirectURL is the callback URL the IdP will redirect to.
// serverBase is the externally-reachable origin of the
// ragabast server (scheme + host [+ port]); the per-provider
// path is appended. serverBase should NOT have a trailing
// slash — RedirectURL appends "/auth/<name>/callback".
func (p OAuthProvider) RedirectURL(serverBase string) string {
	base := strings.TrimRight(serverBase, "/")

	return base + "/auth/" + p.Name + "/callback"
}

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

// OllamaConfig holds configuration for the embedding and chat-completions
// servers. The struct keeps the historical name for backwards-compatible
// config files, but the two URLs are independent: you can point embeddings
// at one server (e.g. Ollama running the nomic-embed-text image) and chat
// at a different OpenAI-compatible server (vLLM, llama.cpp, LM Studio, etc.).
type OllamaConfig struct {
	// BaseURL is the embeddings server endpoint. Speaks the OpenAI
	// /v1/embeddings protocol — supported by Ollama 0.5+, vLLM, llama.cpp
	// --embedding, LM Studio, llama-stack, OpenAI, and any other
	// OpenAI-compatible server. Defaults to a local Ollama instance running
	// the nomic-embed-text image.
	BaseURL string `env:"OLLAMA_BASE_URL" yaml:"base_url"`

	// EmbeddingModel is the model used for embeddings on the embeddings server.
	// Changing this requires updating VectorDB.EmbeddingDimension and
	// re-ingesting all documents (delete data/vectors/ first).
	EmbeddingModel string `env:"OLLAMA_EMBEDDING_MODEL" yaml:"embedding_model"`

	// EmbeddingDimensions is the Matryoshka truncation size. When
	// >0, the embeddings client sends `dimensions: N` with every
	// /v1/embeddings request and warns if the server returns a
	// vector of a different length. Supported by jina v5
	// (32/64/128/256/512/768/1024) and OpenAI text-embedding-3-*
	// (any positive integer). Leave at 0 to disable truncation.
	EmbeddingDimensions int `env:"OLLAMA_EMBEDDING_DIMENSIONS" yaml:"embedding_dimensions"`

	// EmbeddingConcurrency controls the parallel worker pool
	// that GenerateChunkEmbeddings uses for the ingest path.
	// When >1, chunks are split into min(concurrency, n) equal
	// sub-batches and embedded concurrently — useful when the
	// server supports parallel requests (Ollama's
	// OLLAMA_NUM_PARALLEL, vLLM's --max-num-seqs). Default 4
	// matches Ollama's default. Set to 1 to disable the pool.
	EmbeddingConcurrency int `env:"OLLAMA_EMBEDDING_CONCURRENCY" yaml:"embedding_concurrency"`

	// EmbeddingDocPrompt is prepended to every chunk text
	// before the OpenAIEmbeddingClient POSTs to /v1/embeddings
	// on the doc side (GenerateEmbedding, GenerateChunkEmbedding,
	// GenerateChunkEmbeddings). Default empty — preserves the
	// historical raw-text behavior for nomic-embed-text and
	// other prompt-free models. Operators switching to
	// Google's EmbeddingGemma (or another task-prompted
	// model) should set this to Google's recommended
	// `title: none | text:` so the model produces
	// retrieval-optimized document vectors.
	EmbeddingDocPrompt string `env:"OLLAMA_EMBEDDING_DOC_PROMPT" yaml:"embedding_doc_prompt,omitempty"`

	// EmbeddingQueryPrompt is prepended to the user query
	// before the OpenAIEmbeddingClient POSTs to /v1/embeddings
	// on the query side (EmbedQuery). Same default-empty
	// behavior as EmbeddingDocPrompt. For EmbeddingGemma,
	// the recommended value is
	// `task: search result | query:`. The doc and query
	// prompts are independent — operators can tune them
	// separately, including setting one and clearing the
	// other.
	EmbeddingQueryPrompt string `env:"OLLAMA_EMBEDDING_QUERY_PROMPT" yaml:"embedding_query_prompt,omitempty"`

	// ChatBaseURL is the OpenAI-compatible chat completions server endpoint.
	// Defaults to the same local Ollama instance (which exposes
	// /v1/chat/completions from 0.5+), but can point at vLLM, llama.cpp
	// --server, LM Studio, llama-stack, OpenRouter, OpenAI, etc.
	ChatBaseURL string `env:"OLLAMA_CHAT_BASE_URL" yaml:"chat_base_url"`

	// ChatModel is the model name sent to the chat completions server.
	ChatModel string `env:"OLLAMA_CHAT_MODEL" yaml:"chat_model"`

	// APIKey is the default bearer token used for both servers. It is sent
	// as `Authorization: Bearer <key>` to whichever server doesn't have its
	// own override set. Leave empty for unauthenticated local servers.
	APIKey string `env:"OLLAMA_API_KEY" yaml:"api_key"`

	// ChatAPIKey overrides APIKey for the chat completions server only.
	// Useful when the chat provider (e.g. OpenRouter, OpenAI) requires a
	// different token than the embeddings provider.
	ChatAPIKey string `env:"OLLAMA_CHAT_API_KEY" yaml:"chat_api_key"`

	// EmbeddingAPIKey overrides APIKey for the embeddings server only.
	// Useful when the embeddings provider requires a different token than
	// the chat provider.
	EmbeddingAPIKey string `env:"OLLAMA_EMBEDDING_API_KEY" yaml:"embedding_api_key"`

	// Temperature controls LLM sampling. If omitted, the model default is used.
	Temperature *float64 `env:"OLLAMA_TEMPERATURE" yaml:"temperature,omitempty"`

	// Options are model/runtime generation options forwarded to the chat server
	// (e.g. top_p, top_k, num_predict). These are merged into the top-level
	// /v1/chat/completions request body, so any vendor-specific field works.
	Options map[string]any `env:"OLLAMA_OPTIONS_JSON" yaml:"options,omitempty"`

	// Timeout for API calls.
	Timeout time.Duration `env:"OLLAMA_TIMEOUT" yaml:"timeout"`
}

// EmbeddingGemmaConfig holds the embedding-gemma /v2/embed
// server settings. Distinct from OllamaConfig because the
// wire shape is non-standard:
//
//   - Path is /v2/embed, not /v1/embeddings.
//   - Body is {"texts": [...]}, not {"input": [...], "model": "..."}.
//   - No Authorization header.
//   - No `dimensions` request field — the server picks the
//     dimension; configure vectordb.embedding_dimension to
//     match what the server returns.
//
// Only consulted when Config.EmbeddingProvider is
// "embedding_gemma". Empty / missing BaseURL is a config
// error in that case (see Config.Validate).
type EmbeddingGemmaConfig struct {
	// BaseURL is the server root. The client appends
	// "/v2/embed" itself; trailing /v1 segments are
	// tolerated and stripped (matches the OpenAI
	// client's normalization so operators can paste
	// either form).
	BaseURL string `env:"EMBEDDING_GEMMA_BASE_URL" yaml:"base_url"`

	// Timeout is the per-request HTTP timeout. Default
	// 30s when <= 0.
	Timeout time.Duration `env:"EMBEDDING_GEMMA_TIMEOUT" yaml:"timeout"`

	// EmbeddingDimensions is the configured dimension
	// the operator expects the server to return. 0
	// disables the mismatch check. The /v2/embed endpoint
	// does NOT accept a `dimensions` request field —
	// operators have to match the server-side setting
	// via vectordb.embedding_dimension. This field is
	// only used to detect mismatches and log a one-shot
	// warning if the server returns a different size.
	EmbeddingDimensions int `env:"EMBEDDING_GEMMA_DIMENSIONS" yaml:"embedding_dimensions"`

	// EmbeddingConcurrency controls the parallel
	// worker pool that GenerateChunkEmbeddings uses on
	// the ingest path. Same semantics as
	// OllamaConfig.EmbeddingConcurrency — chunks are
	// split into min(concurrency, len(chunks)) equal
	// sub-batches and processed concurrently. Default 1
	// (sequential). Set higher to match the upstream
	// server's request parallelism.
	EmbeddingConcurrency int `env:"EMBEDDING_GEMMA_CONCURRENCY" yaml:"embedding_concurrency"`

	// EmbeddingDocPrompt is prepended to every chunk
	// text before POSTing to /v2/embed on the doc side.
	// Default empty — preserves the historical raw-text
	// behavior for non-prompted models. Operators
	// using Google's embedding-gemma should set this to
	// "title: none | text: " so the model produces
	// retrieval-optimized document vectors.
	EmbeddingDocPrompt string `env:"EMBEDDING_GEMMA_DOC_PROMPT" yaml:"embedding_doc_prompt,omitempty"`

	// EmbeddingQueryPrompt is prepended to the user
	// query before POSTing to /v2/embed on the query
	// side (EmbedQuery). Same default-empty behavior
	// as EmbeddingDocPrompt. For embedding-gemma the
	// recommended value is "task: search result |
	// query: ". Independent from EmbeddingDocPrompt so
	// operators can tune the two sides separately.
	EmbeddingQueryPrompt string `env:"EMBEDDING_GEMMA_QUERY_PROMPT" yaml:"embedding_query_prompt,omitempty"`
}

// VectorDBConfig holds vector database configuration.
type VectorDBConfig struct {
	// Persistence directory.
	PersistenceDir string `env:"VECTOR_DB_DIR" yaml:"persistence_dir"`

	// KeywordIndexDir is the on-disk directory for the bleve
	// keyword search index. When empty (the default), the
	// index lives at <PersistenceDir>/search. Operators can
	// point this at a different path to put the keyword
	// index on a local SSD while keeping the vector DB on a
	// shared filesystem — bleve's mmap reads perform poorly
	// or fail outright on NFS.
	KeywordIndexDir string `env:"VECTOR_DB_KEYWORD_INDEX_DIR" yaml:"keyword_index_dir,omitempty"`

	// CollectionName is the name of the chromem-go collection.
	CollectionName string `env:"VECTOR_DB_COLLECTION" yaml:"collection_name"`

	// EmbeddingDimension is the expected dimension of embeddings.
	EmbeddingDimension int `env:"VECTOR_DB_DIMENSION" yaml:"embedding_dimension"`
}

// ServerConfig holds web server configuration.
type ServerConfig struct {
	// Address to bind the server.
	Address string `env:"SERVER_ADDRESS" yaml:"address"`

	// Port to bind the server.
	Port int `env:"SERVER_PORT" yaml:"port"`

	// PublicURL is the externally-reachable origin
	// (scheme + host [+ port]) the IdP redirects back
	// to after a successful OAuth login. When set, it
	// overrides the URL derived from Address + Port —
	// required for any install behind a reverse proxy
	// (Traefik / nginx / Caddy / an L7 cloud LB) where
	// the bind address is a private hostname but the
	// public URL is something else entirely.
	//
	// The callback URL the IdP needs to whitelist is
	// `<PublicURL>/auth/<provider-name>/callback` for
	// every configured provider. deriveServerBase
	// trims any trailing slash so concatenation never
	// produces a double slash.
	//
	// Must be a fully-qualified http/https URL with a
	// non-empty host when set. Empty (the default)
	// preserves the historical Address+Port
	// derivation, so single-host installs and
	// localhost dev keep working with zero config.
	//
	// Env: SERVER_PUBLIC_URL. YAML: server.public_url.
	PublicURL string `env:"SERVER_PUBLIC_URL" yaml:"public_url,omitempty"`

	// Enable CORS.
	EnableCORS bool `env:"SERVER_ENABLE_CORS" yaml:"enable_cors"`

	// CORSOrigins is the explicit allow-list of origins the
	// server will echo in Access-Control-Allow-Origin. Wildcard
	// "*" is supported for trusted local-only deployments but
	// is NOT the default — leaving it unset (empty slice)
	// disables cross-origin browser requests entirely, which
	// is the safe default for a single-tenant tool exposed on
	// 127.0.0.1.
	CORSOrigins []string `env:"SERVER_CORS_ORIGINS" yaml:"cors_origins,omitempty"`

	// AuthToken is the shared bearer-token secret the web
	// layer requires on every state-changing or read endpoint
	// when it is non-empty. Empty (the default) keeps the
	// server open so local single-user installs work without
	// configuration. Operators exposing ragabast on a
	// non-loopback interface MUST set this — without it, any
	// network-reachable client can ingest, query, and delete
	// documents.
	//
	// Wire format: clients send `Authorization: Bearer <token>`.
	// Comparison uses constant-time equality (subtle.ConstantTimeCompare)
	// so the endpoint cannot be used as a timing oracle.
	//
	// Kept for backward compatibility; operators running a
	// fleet of internal consumers should prefer AuthTokens
	// (the modern list form) which supports rotation without
	// coordinated outages and per-consumer attribution
	// labels. EffectiveAuthTokens() merges both forms with
	// dedup so an existing single-token config continues to
	// work after the operator adds AuthTokens.
	AuthToken string `env:"SERVER_AUTH_TOKEN" yaml:"auth_token,omitempty"`

	// AuthTokens is the modern list of accepted bearer
	// tokens. Each entry may be a bare string (the common
	// case — no label) or a {value, label} struct when the
	// operator wants per-consumer attribution in the access
	// log. Label has no security meaning — it is purely
	// operational attribution.
	//
	// Token rotation: list both old and new during a grace
	// window, then remove the old once all consumers have
	// migrated. EffectiveAuthTokens handles dedup so adding
	// the same token to both this list and AuthToken (the
	// singular form) does not double-count.
	AuthTokens []AuthToken `env:"SERVER_AUTH_TOKENS" yaml:"auth_tokens,omitempty"`

	// RateLimitPerMinute is the per-client-IP token-bucket
	// rate applied to the LLM-backed endpoints. Zero
	// disables the limiter (the local-dev default). The
	// bucket is refilled continuously; the effective
	// sustained rate is the configured value.
	RateLimitPerMinute int `env:"SERVER_RATE_LIMIT_PER_MINUTE" yaml:"rate_limit_per_minute,omitempty"`

	// RateLimitBurst is the maximum number of immediate
	// requests a single client IP can issue before the
	// per-minute rate kicks in. Defaults to 5; raise it
	// only if the operator's browser interaction trips
	// the limiter, not for "tolerance" reasons.
	RateLimitBurst int `env:"SERVER_RATE_LIMIT_BURST" yaml:"rate_limit_burst,omitempty"`

	// MaxIngestDocumentBytes caps the size of a single
	// document accepted by the ingest endpoints. The 10 MiB
	// request-body limit (H-2) protects the server from
	// memory exhaustion in general, but a single ingest
	// request is also one document — which would still pin
	// the embedding model and OOM the chunker. The default
	// (1 MiB) is generous for any plausible single
	// docbuilder document; operators working with unusually
	// large source documents can raise it.
	MaxIngestDocumentBytes int `env:"SERVER_MAX_INGEST_DOCUMENT_BYTES" yaml:"max_ingest_document_bytes,omitempty"`

	// AsyncIngestQueueDir is the on-disk directory the async
	// ingest queue persists jobs to. Empty disables async
	// ingest (the /api/ingest/async endpoint returns 503).
	// Default: <data_dir>/jobs. The directory is created
	// with mode 0700 at server start; each job file is 0600.
	AsyncIngestQueueDir string `env:"SERVER_ASYNC_INGEST_QUEUE_DIR" yaml:"async_ingest_queue_dir,omitempty"`

	// AsyncIngestWorkers caps the worker-pool concurrency
	// for the async ingest queue. 0 falls back to 1.
	// Default 5 — matches the historical IngestLimiter
	// concurrency for the sync path; raise it to match the
	// embeddings server's request parallelism.
	AsyncIngestWorkers int `env:"SERVER_ASYNC_INGEST_WORKERS" yaml:"async_ingest_workers,omitempty"`

	// AsyncIngestCompletedJobTTL is the maximum age of a
	// completed .json file before the cleanup sweep
	// removes it. Default 168h (7 days). Set to 0 to
	// disable cleanup entirely.
	AsyncIngestCompletedJobTTL time.Duration `env:"SERVER_ASYNC_INGEST_COMPLETED_JOB_TTL" yaml:"async_ingest_completed_job_ttl,omitempty"`

	// AsyncIngestFailedJobTTL is the maximum age of a failed
	// .json file before the cleanup sweep removes it.
	// Default 720h (30 days) — failed jobs default to longer
	// retention than completed ones so operators have time
	// to investigate. Set to 0 to disable.
	AsyncIngestFailedJobTTL time.Duration `env:"SERVER_ASYNC_INGEST_FAILED_JOB_TTL" yaml:"async_ingest_failed_job_ttl,omitempty"`

	// AsyncIngestCleanupInterval is how often the cleanup
	// sweep runs in the background. Default 1h. Set to 0
	// to disable the background sweep (you can still call
	// RunCleanup manually).
	AsyncIngestCleanupInterval time.Duration `env:"SERVER_ASYNC_INGEST_CLEANUP_INTERVAL" yaml:"async_ingest_cleanup_interval,omitempty"`

	// ReadTimeout for HTTP requests.
	ReadTimeout time.Duration `env:"SERVER_READ_TIMEOUT" yaml:"read_timeout"`

	// WriteTimeout for HTTP responses.
	WriteTimeout time.Duration `env:"SERVER_WRITE_TIMEOUT" yaml:"write_timeout"`

	// MCPHTTPEnabled mounts the Model Context Protocol
	// streamable-HTTP server at /mcp on the same listen
	// port. Bearer-token auth via the existing
	// server.auth_tokens applies. Off by default — opt
	// in by setting mcp_http_enabled: true or
	// SERVER_MCP_HTTP_ENABLED=true.
	MCPHTTPEnabled bool `env:"SERVER_MCP_HTTP_ENABLED" yaml:"mcp_http_enabled,omitempty"`
}

// ListenAddr returns an address suitable for net/http Server.Addr.
//
// Rules:
// - If Address already includes a port (e.g. ":8080" or "0.0.0.0:8080"), it is returned as-is.
// - Otherwise Address is treated as a host and combined with Port.
// - If Port is 0, 8080 is used.
//
// Note: Port range validation (1-65535) is enforced by Config.Validate().
func (s ServerConfig) ListenAddr() string {
	addr := strings.TrimSpace(s.Address)
	port := s.Port
	if port == 0 {
		port = 8080
	}

	if addr == "" {
		addr = "0.0.0.0"
	}

	// If addr already includes a port, keep it.
	if _, _, err := net.SplitHostPort(addr); err == nil {
		return addr
	}

	return net.JoinHostPort(addr, strconv.Itoa(port))
}

// ProcessingConfig holds document processing configuration.
type ProcessingConfig struct {
	// MaxChunkSize is the maximum size of a chunk in characters.
	MaxChunkSize int `env:"PROCESSING_MAX_CHUNK_SIZE" yaml:"max_chunk_size"`

	// MinChunkSize is the minimum size of a chunk in characters.
	MinChunkSize int `env:"PROCESSING_MIN_CHUNK_SIZE" yaml:"min_chunk_size"`

	// ChunkOverlap is the number of characters to overlap between chunks.
	ChunkOverlap int `env:"PROCESSING_CHUNK_OVERLAP" yaml:"chunk_overlap"`
}

// PathsConfig holds file path configuration.
type PathsConfig struct {
	// DataDir is the base directory for all data.
	DataDir string `env:"DATA_DIR" yaml:"data_dir"`

	// TemplatesDir is an optional override. Empty (the default)
	// tells the web server to use the templates embedded into
	// the binary (see internal/web/templates.go). Set this to
	// point at a directory of *.html files to ship a custom
	// template set without rebuilding.
	TemplatesDir string `env:"TEMPLATES_DIR" yaml:"templates_dir"`
}

// DefaultConfig returns a configuration with sensible defaults.
func DefaultConfig() *Config {
	// Get current working directory.
	wd, _ := os.Getwd()
	if wd == "" {
		wd = "."
	}

	defaultTemp := 0.1

	return &Config{
		// EmbeddingProvider defaults to empty, which the
		// factory resolves to "openai" — the historical
		// Ollama /v1/embeddings path. Operators switching
		// to the EmbeddingGemma /v2/embed wire format set
		// this explicitly to "embedding_gemma" (env:
		// EMBEDDING_PROVIDER, YAML: embedding_provider).
		EmbeddingProvider: "",
		Ollama: OllamaConfig{
			BaseURL:              "http://localhost:11434",
			ChatBaseURL:          "http://localhost:11434",
			ChatModel:            "gemma:2b",
			EmbeddingModel:       "nomic-embed-text:v1.5",
			Timeout:              30 * time.Second,
			EmbeddingConcurrency: 4,
			// RAG-friendly defaults: low temperature + conservative sampling.
			Temperature: &defaultTemp,
			Options: map[string]any{
				"top_k":       20,
				"top_p":       0.8,
				"min_p":       0.05,
				"num_predict": 512,
			},
		},
		// EmbeddingGemma is empty by default. Validate
		// only checks BaseURL when EmbeddingProvider is
		// "embedding_gemma", so a default config still
		// passes Validate unchanged.
		EmbeddingGemma: EmbeddingGemmaConfig{},
		VectorDB: VectorDBConfig{
			PersistenceDir:     filepath.Join(wd, "data", "vectors"),
			CollectionName:     "ragabast",
			EmbeddingDimension: 768, // nomic-embed-text-v1.5 dimension
		},
		Server: ServerConfig{
			Address:                    "0.0.0.0",
			Port:                       8080,
			EnableCORS:                 true,
			MaxIngestDocumentBytes:     1 << 20, // 1 MiB
			ReadTimeout:                15 * time.Second,
			WriteTimeout:               15 * time.Second,
			AsyncIngestCompletedJobTTL: 168 * time.Hour, // 7 days
			AsyncIngestFailedJobTTL:    720 * time.Hour, // 30 days
			AsyncIngestCleanupInterval: 1 * time.Hour,
		},
		Processing: ProcessingConfig{
			MaxChunkSize: 2000,
			MinChunkSize: 300,
			ChunkOverlap: 150,
		},
		Paths: PathsConfig{
			DataDir: filepath.Join(wd, "data"),
			// TemplatesDir defaults to empty, which makes
			// internal/web.NewServer load the templates that
			// ship inside the binary (//go:embed). Set
			// TEMPLATES_DIR=... or `paths.templates_dir:` to
			// override with a custom on-disk set.
			TemplatesDir: "",
		},
	}
}

// Load loads configuration.
//
// Precedence:
//  1. If path is provided, it is used.
//  2. If DefaultConfigYML exists in the working directory, it is used.
//  3. If DefaultConfigYAML exists in the working directory, it is used.
//  4. Otherwise, defaults are used.
//
// In all cases, environment variable overrides are applied (see ApplyEnvOverrides).
func Load(path string) (*Config, error) {
	if strings.TrimSpace(path) != "" {
		return LoadConfig(path)
	}

	if fileExists(DefaultConfigYML) {
		return LoadConfig(DefaultConfigYML)
	}
	if fileExists(DefaultConfigYAML) {
		return LoadConfig(DefaultConfigYAML)
	}

	cfg := DefaultConfig()
	cfg.ApplyEnvOverrides()
	return cfg, nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err == nil {
		return !info.IsDir()
	}
	return !errors.Is(err, os.ErrNotExist)
}

// LoadConfig loads configuration from a YAML file.
func LoadConfig(path string) (*Config, error) {
	path = resolvePath(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	cfg := DefaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Apply environment variable overrides.
	cfg.ApplyEnvOverrides()

	return cfg, nil
}

// resolvePath expands a leading "~/" or "~" to the user's home
// directory so users can write `--config ~/.ragabast.yml` from
// a shell that does not auto-expand tilde (env-var assignments,
// quoted strings, systemd units, Docker exec).
//
// Only the bare-username form is supported; "~user/path" is left
// literal so os.ReadFile surfaces a clear "no such file" error
// rather than silently mangling the path. Other inputs (absolute
// paths, relative paths, empty strings) pass through unchanged.
//
// os.UserHomeDir() is best-effort: if HOME / USERPROFILE lookup
// fails for any reason, resolvePath returns the input unchanged
// rather than producing a wrong path.
func resolvePath(path string) string {
	if path == "" || path[0] != '~' {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}

// SaveConfig saves the configuration to a YAML file.
//
// Permission: the file is written with mode 0o600 (owner
// read/write only). config.yml may carry bearer tokens
// (server.auth_token) and LLM API keys (ollama.api_key);
// the default umask on Linux is 022 which would otherwise
// produce a world-readable file. SaveConfig overrides the
// umask explicitly so operators do not have to remember to
// chmod after generating the config.
//
// On overwrite (the file already exists), SaveConfig first
// chmods the existing file to 0o600 before truncating it.
// os.WriteFile alone would NOT change permissions on an
// existing file; a fresh write with mode 0o600 only applies
// when the file is being created.
//
// The directory is still created with 0o755 (readable to
// all) so a Docker-style bind mount can read config files
// the operator placed there; the file itself is owner-only.
func (c *Config) SaveConfig(path string) error {
	// Ensure directory exists.
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Force 0o600 on any existing file before we overwrite
	// it. Otherwise the new WriteFile below would inherit
	// whatever mode the existing file had — and the
	// `config init --force` overwrite path is exactly the
	// time operators are most likely to carry tokens in
	// the file.
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
		if err := os.Chmod(path, 0o600); err != nil {
			return fmt.Errorf("failed to chmod existing config: %w", err)
		}
	}

	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// ApplyEnvOverrides applies environment variable overrides to config fields.
func (c *Config) ApplyEnvOverrides() {
	// Ragabast config.
	if v := os.Getenv("RAGABAST_DOCBUILDER_BASE_URL"); v != "" {
		c.Ragabast.DocbuilderBaseURL = v
	}
	if v, ok := os.LookupEnv("RAGABAST_QUERY_CACHE_SIZE"); ok {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			c.Ragabast.QueryCacheSize = n
		}
	}
	if v := os.Getenv("RAGABAST_QUERY_CACHE_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.Ragabast.QueryCacheTTL = d
		}
	}
	if v := os.Getenv("RAGABAST_LOG_CHAT_REQUESTS"); v != "" {
		// Accept the common truthy spellings so an operator
		// doesn't need to know our exact format. Empty string
		// is treated as false (the default).
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "t", "yes", "y", "on":
			c.Ragabast.LogChatRequests = true
		case "0", "false", "f", "no", "n", "off":
			c.Ragabast.LogChatRequests = false
		}
	}

	// Embedding provider selector. Empty means the
	// historical OpenAI-compat path; "embedding_gemma"
	// switches to the EmbeddingGemma /v2/embed client.
	if v := os.Getenv("EMBEDDING_PROVIDER"); v != "" {
		c.EmbeddingProvider = v
	}

	// EmbeddingGemma /v2/embed server. Only consulted
	// when EmbeddingProvider == "embedding_gemma".
	if v := os.Getenv("EMBEDDING_GEMMA_BASE_URL"); v != "" {
		c.EmbeddingGemma.BaseURL = v
	}
	if v := os.Getenv("EMBEDDING_GEMMA_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.EmbeddingGemma.Timeout = d
		}
	}
	if v, ok := os.LookupEnv("EMBEDDING_GEMMA_DIMENSIONS"); ok {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			c.EmbeddingGemma.EmbeddingDimensions = n
		}
	}
	if v, ok := os.LookupEnv("EMBEDDING_GEMMA_CONCURRENCY"); ok {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			c.EmbeddingGemma.EmbeddingConcurrency = n
		}
	}
	if v, ok := os.LookupEnv("EMBEDDING_GEMMA_DOC_PROMPT"); ok {
		// LookupEnv (not Getenv) so an explicitly-empty env
		// var clears the field — same convention as the
		// Ollama embedding prompts.
		c.EmbeddingGemma.EmbeddingDocPrompt = v
	}
	if v, ok := os.LookupEnv("EMBEDDING_GEMMA_QUERY_PROMPT"); ok {
		c.EmbeddingGemma.EmbeddingQueryPrompt = v
	}

	// Ollama config.
	if url := os.Getenv("OLLAMA_BASE_URL"); url != "" {
		c.Ollama.BaseURL = url
	}
	if model := os.Getenv("OLLAMA_EMBEDDING_MODEL"); model != "" {
		c.Ollama.EmbeddingModel = model
	}
	if url := os.Getenv("OLLAMA_CHAT_BASE_URL"); url != "" {
		c.Ollama.ChatBaseURL = url
	}
	if model := os.Getenv("OLLAMA_CHAT_MODEL"); model != "" {
		c.Ollama.ChatModel = model
	}
	if key := os.Getenv("OLLAMA_API_KEY"); key != "" {
		c.Ollama.APIKey = key
	}
	if key := os.Getenv("OLLAMA_CHAT_API_KEY"); key != "" {
		c.Ollama.ChatAPIKey = key
	}
	if key := os.Getenv("OLLAMA_EMBEDDING_API_KEY"); key != "" {
		c.Ollama.EmbeddingAPIKey = key
	}
	if timeout := os.Getenv("OLLAMA_TIMEOUT"); timeout != "" {
		if d, err := time.ParseDuration(timeout); err == nil {
			c.Ollama.Timeout = d
		}
	}
	if temp := os.Getenv("OLLAMA_TEMPERATURE"); temp != "" {
		if v, err := strconv.ParseFloat(strings.TrimSpace(temp), 64); err == nil {
			c.Ollama.Temperature = &v
		}
	}
	if v, ok := os.LookupEnv("OLLAMA_EMBEDDING_DOC_PROMPT"); ok {
		// LookupEnv (not Getenv) so an explicitly-empty env
		// var clears the field — operators can disable the
		// prompt by exporting the variable to "" without
		// unsetting it. This matches how every other
		// config knob in this codebase behaves: presence
		// of the env var means "the operator chose this
		// value", absence means "use the YAML / default".
		c.Ollama.EmbeddingDocPrompt = v
	}
	if v, ok := os.LookupEnv("OLLAMA_EMBEDDING_QUERY_PROMPT"); ok {
		c.Ollama.EmbeddingQueryPrompt = v
	}
	if raw := os.Getenv("OLLAMA_OPTIONS_JSON"); strings.TrimSpace(raw) != "" {
		var m map[string]any
		if err := json.Unmarshal([]byte(raw), &m); err == nil {
			if c.Ollama.Options == nil {
				c.Ollama.Options = map[string]any{}
			}
			maps.Copy(c.Ollama.Options, m)
		}
	}

	// VectorDB config.
	if dir := os.Getenv("VECTOR_DB_DIR"); dir != "" {
		c.VectorDB.PersistenceDir = dir
	}
	if coll := os.Getenv("VECTOR_DB_COLLECTION"); coll != "" {
		c.VectorDB.CollectionName = coll
	}

	// Server config.
	if addr := os.Getenv("SERVER_ADDRESS"); addr != "" {
		c.Server.Address = addr
	}
	if port := os.Getenv("SERVER_PORT"); port != "" {
		if p, err := strconv.Atoi(port); err == nil {
			c.Server.Port = p
		}
	}
	if v := os.Getenv("SERVER_PUBLIC_URL"); v != "" {
		c.Server.PublicURL = v
	}
	if v := os.Getenv("SERVER_AUTH_TOKEN"); v != "" {
		c.Server.AuthToken = v
	}
	if v := os.Getenv("SERVER_AUTH_TOKENS"); v != "" {
		// Comma-separated list of bare tokens. Labels are
		// not expressible in env vars — operators needing
		// per-token labels should set them via YAML or
		// mount a config file from a secret manager.
		parts := strings.Split(v, ",")
		out := make([]AuthToken, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, AuthToken{Value: p})
			}
		}
		c.Server.AuthTokens = out
	}
	if v := os.Getenv("SERVER_RATE_LIMIT_PER_MINUTE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			c.Server.RateLimitPerMinute = n
		}
	}
	if v := os.Getenv("SERVER_RATE_LIMIT_BURST"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			c.Server.RateLimitBurst = n
		}
	}
	if v := os.Getenv("SERVER_MAX_INGEST_DOCUMENT_BYTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.Server.MaxIngestDocumentBytes = n
		}
	}
	if v := os.Getenv("SERVER_ASYNC_INGEST_QUEUE_DIR"); v != "" {
		c.Server.AsyncIngestQueueDir = v
	}
	if v := os.Getenv("SERVER_ASYNC_INGEST_WORKERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.Server.AsyncIngestWorkers = n
		}
	}
	if v := os.Getenv("SERVER_ASYNC_INGEST_COMPLETED_JOB_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.Server.AsyncIngestCompletedJobTTL = d
		}
	}
	if v := os.Getenv("SERVER_ASYNC_INGEST_FAILED_JOB_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.Server.AsyncIngestFailedJobTTL = d
		}
	}
	if v := os.Getenv("SERVER_ASYNC_INGEST_CLEANUP_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.Server.AsyncIngestCleanupInterval = d
		}
	}
	if v := os.Getenv("SERVER_CORS_ORIGINS"); v != "" {
		// Comma-separated origin list. Empty entries are
		// dropped; whitespace around entries is trimmed.
		parts := strings.Split(v, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, p)
			}
		}
		c.Server.CORSOrigins = out
	}

	// Paths config.
	if dir := os.Getenv("DATA_DIR"); dir != "" {
		c.Paths.DataDir = dir
	}
	if dir := os.Getenv("TEMPLATES_DIR"); dir != "" {
		c.Paths.TemplatesDir = dir
	}

	// Auth config. AUTH_PROVIDERS_JSON is a JSON array of
	// OAuthProvider objects — the env-var equivalent of the
	// YAML auth.providers list. Operators who need per-field
	// env vars (one provider per variable) should use YAML
	// or mount a config file from a secret manager.
	if raw := os.Getenv("AUTH_PROVIDERS_JSON"); strings.TrimSpace(raw) != "" {
		var providers []OAuthProvider
		if err := json.Unmarshal([]byte(raw), &providers); err == nil {
			c.Auth.Providers = providers
		}
	}
	if v := os.Getenv("AUTH_SESSION_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.Auth.SessionTTL = d
		}
	}
	if v := os.Getenv("AUTH_COOKIE_NAME"); v != "" {
		c.Auth.CookieName = v
	}
}

// validateDocbuilderBaseURL checks that the configured
// docbuilder base URL is either empty (the "no permalinks"
// mode) or a syntactically valid http/https URL with a
// non-empty host.
//
// The field is trimmed before validation AND persisted
// back trimmed so synthesized permalinks never carry a
// stray space. Trim-and-mutate is intentional: the only
// legitimate whitespace around a URL is from copy/paste,
// and silently passing " http://x.com/ " through would
// produce " http://x.com/_uid/abc/" links in the chat
// UI — broken and visibly wrong.
//
// A specific scheme allow-list (http, https) protects
// against a more dangerous silent failure than a typo:
// a javascript: or file:// scheme would render the
// permalink as either an XSS vector or a path disclosure.
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

// AuthToken is one configured bearer credential. Value is
// the opaque secret compared (constant-time) against the
// Authorization header; Label is optional human-readable
// attribution surfaced in the access log under auth_label.
//
// Label has no security meaning — it does not grant any
// privilege, and two tokens may share a label. A blank
// label is allowed.
//
// UnmarshalYAML accepts both the bare-string form
// (`auth_tokens: ["secret-a", "secret-b"]`) and the
// explicit value+label form (`auth_tokens: [{value: ...,
// label: ...}]`) so operators can mix per-token in the
// same list — useful during a rotation grace window where
// new tokens are labeled and old ones aren't.
type AuthToken struct {
	Value string `yaml:"value,omitempty"`
	Label string `yaml:"label,omitempty"`
}

// UnmarshalYAML accepts both the scalar form (`"secret"`) and
// the mapping form (`{value: "secret", label: "x"}`) for
// AuthToken. Operators are expected to mix both freely; this
// is the documented behavior in the config docs.
func (t *AuthToken) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		t.Value = node.Value
		return nil
	}
	var r struct {
		Value string `yaml:"value"`
		Label string `yaml:"label"`
	}
	if err := node.Decode(&r); err != nil {
		return fmt.Errorf("auth_token entry: %w", err)
	}
	if r.Value == "" {
		return errors.New("auth_token entry: value is required")
	}
	t.Value = r.Value
	t.Label = r.Label
	return nil
}

// EffectiveAuthTokens returns the union of AuthToken
// (singular, kept for backward compatibility) and
// AuthTokens (the modern list form). Tokens are deduped
// by Value so an operator who lists the same token in
// both places (typically during a rotation grace window)
// gets exactly one match and the constant-time compare
// loop does not double-bill.
func (s ServerConfig) EffectiveAuthTokens() []AuthToken {
	out := make([]AuthToken, 0, len(s.AuthTokens)+1)
	seen := make(map[string]bool, len(out))
	if s.AuthToken != "" {
		out = append(out, AuthToken{Value: s.AuthToken})
		seen[s.AuthToken] = true
	}
	for _, t := range s.AuthTokens {
		if t.Value == "" || seen[t.Value] {
			continue
		}
		out = append(out, t)
		seen[t.Value] = true
	}
	return out
}

func validateOllamaOptions(opts map[string]any) error {
	if len(opts) == 0 {
		return nil
	}

	getFloat := func(v any) (float64, bool) {
		switch t := v.(type) {
		case float64:
			return t, true
		case float32:
			return float64(t), true
		case int:
			return float64(t), true
		case int64:
			return float64(t), true
		case uint:
			return float64(t), true
		case uint64:
			return float64(t), true
		default:
			return 0, false
		}
	}

	getInt := func(v any) (int64, bool) {
		switch t := v.(type) {
		case int:
			return int64(t), true
		case int64:
			return t, true
		case uint:
			return int64(t), true
		case uint64:
			return int64(t), true
		case float64:
			return int64(t), true
		default:
			return 0, false
		}
	}

	if v, ok := opts["top_p"]; ok {
		if f, ok := getFloat(v); ok {
			if f < 0 || f > 1 {
				return errors.New("ollama.options.top_p must be between 0 and 1")
			}
		}
	}
	if v, ok := opts["min_p"]; ok {
		if f, ok := getFloat(v); ok {
			if f < 0 || f > 1 {
				return errors.New("ollama.options.min_p must be between 0 and 1")
			}
		}
	}
	if v, ok := opts["top_k"]; ok {
		if i, ok := getInt(v); ok {
			if i < 0 {
				return errors.New("ollama.options.top_k must be >= 0")
			}
		}
	}
	if v, ok := opts["num_predict"]; ok {
		if i, ok := getInt(v); ok {
			if i <= 0 {
				return errors.New("ollama.options.num_predict must be > 0")
			}
		}
	}
	if v, ok := opts["num_ctx"]; ok {
		if i, ok := getInt(v); ok {
			if i <= 0 {
				return errors.New("ollama.options.num_ctx must be > 0")
			}
		}
	}

	return nil
}

// EffectiveChatAPIKey returns the bearer token to use for the chat
// completions server. ChatAPIKey wins when set; otherwise the global APIKey
// is used. Returns the empty string when no auth is configured.
func (o *OllamaConfig) EffectiveChatAPIKey() string {
	if o.ChatAPIKey != "" {
		return o.ChatAPIKey
	}
	return o.APIKey
}

// EffectiveEmbeddingAPIKey returns the bearer token to use for the
// embeddings server. EmbeddingAPIKey wins when set; otherwise the global
// APIKey is used. Returns the empty string when no auth is configured.
func (o *OllamaConfig) EffectiveEmbeddingAPIKey() string {
	if o.EmbeddingAPIKey != "" {
		return o.EmbeddingAPIKey
	}
	return o.APIKey
}

// validateAuth runs the cross-provider validation: each
// provider validates itself, then we check for duplicate
// names and reserved-name collisions. Returned error is a
// single combined message suitable for Validate()'s errs
// list (joined with newlines).
func (c *Config) validateAuth() error {
	if len(c.Auth.Providers) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(c.Auth.Providers))

	var errs []string
	for i, p := range c.Auth.Providers {
		if err := p.Validate(); err != nil {
			errs = append(errs, err.Error())
		}
		if reservedProviderNames[p.Name] {
			errs = append(errs, fmt.Sprintf("auth.providers[%d].name %q is reserved (login, logout, me are built-in routes)", i, p.Name))
		}
		if seen[p.Name] {
			errs = append(errs, fmt.Sprintf("auth.providers[%d].name %q is a duplicate of an earlier entry", i, p.Name))
		}
		seen[p.Name] = true
	}
	if len(errs) == 0 {
		return nil
	}

	return fmt.Errorf("%s", strings.Join(errs, "; "))
}

// Validate checks if the configuration is valid.
func (c *Config) Validate() error {
	var errs []string

	// Validate embedding provider selection. The default
	// (empty) is the OpenAI-compat path; the only other
	// supported value is "embedding_gemma".
	if c.EmbeddingProvider != "" && c.EmbeddingProvider != "openai" && c.EmbeddingProvider != "embedding_gemma" {
		errs = append(errs, fmt.Sprintf(
			"embedding_provider %q is not recognized (expected \"openai\" or \"embedding_gemma\")",
			c.EmbeddingProvider,
		))
	}

	// When the embedding_gemma provider is selected, its
	// base URL is required. The OpenAI-compat path is
	// always validated below against ollama.* — we don't
	// duplicate that here.
	if c.EmbeddingProvider == "embedding_gemma" && c.EmbeddingGemma.BaseURL == "" {
		errs = append(errs, "embedding_gemma.base_url is required when embedding_provider is \"embedding_gemma\"")
	}
	if c.EmbeddingGemma.Timeout < 0 {
		errs = append(errs, "embedding_gemma.timeout must be >= 0")
	}
	if c.EmbeddingGemma.EmbeddingDimensions < 0 {
		errs = append(errs, "embedding_gemma.embedding_dimensions must be >= 0")
	}
	if c.EmbeddingGemma.EmbeddingConcurrency < 0 {
		errs = append(errs, "embedding_gemma.embedding_concurrency must be >= 0")
	}

	// Validate Ollama config.
	if c.Ollama.BaseURL == "" {
		errs = append(errs, "ollama.base_url is required")
	}
	if c.Ollama.EmbeddingModel == "" {
		errs = append(errs, "ollama.embedding_model is required")
	}
	if c.Ollama.ChatBaseURL == "" {
		errs = append(errs, "ollama.chat_base_url is required")
	}
	if c.Ollama.ChatModel == "" {
		errs = append(errs, "ollama.chat_model is required")
	}
	if c.Ollama.Timeout <= 0 {
		errs = append(errs, "ollama.timeout must be positive")
	}
	if c.Ollama.Temperature != nil {
		if *c.Ollama.Temperature < 0 || *c.Ollama.Temperature > 2 {
			errs = append(errs, "ollama.temperature must be between 0 and 2")
		}
	}
	if err := validateOllamaOptions(c.Ollama.Options); err != nil {
		errs = append(errs, err.Error())
	}

	// Validate VectorDB config.
	if c.VectorDB.PersistenceDir == "" {
		errs = append(errs, "vectordb.persistence_dir is required")
	}
	if c.VectorDB.CollectionName == "" {
		errs = append(errs, "vectordb.collection_name is required")
	}
	if c.VectorDB.EmbeddingDimension <= 0 {
		errs = append(errs, "vectordb.embedding_dimension must be positive")
	}

	// Validate Server config.
	if c.Server.Address == "" {
		errs = append(errs, "server.address is required")
	}
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		errs = append(errs, "server.port must be between 1 and 65535")
	}
	if c.Server.PublicURL != "" {
		// When set, must be a syntactically valid http/https
		// URL with a non-empty host. Empty host = "we'll
		// resolve at request time", which is the wrong
		// failure mode here — catch it at config time.
		// We don't pin a scheme preference: an operator
		// proxying TLS off to Traefik but running ragabast
		// over plain http (the common k8s ingress setup)
		// legitimately wants http://...:port in the
		// callback URL to match what the IdP was registered
		// with.
		u, parseErr := url.Parse(c.Server.PublicURL)
		if parseErr != nil {
			errs = append(errs, fmt.Sprintf("server.public_url is not a valid URL: %s", parseErr.Error()))
		} else if u.Scheme != "http" && u.Scheme != "https" {
			errs = append(errs, fmt.Sprintf("server.public_url scheme %q is not one of: http, https", u.Scheme))
		} else if u.Host == "" {
			errs = append(errs, "server.public_url must include a host")
		}
	}
	if c.Server.MaxIngestDocumentBytes < 0 {
		errs = append(errs, "server.max_ingest_document_bytes must be >= 0")
	}
	if c.Server.AsyncIngestWorkers < 0 {
		errs = append(errs, "server.async_ingest_workers must be >= 0")
	}

	// Validate Ragabast config.
	if err := validateAndTrimDocbuilderBaseURL(c); err != nil {
		errs = append(errs, err.Error())
	}

	// Validate Auth config: each provider independently,
	// then the cross-provider checks (duplicate names,
	// reserved names). Empty Auth.Providers is valid —
	// bearer-token / open-access mode keeps working.
	if err := c.validateAuth(); err != nil {
		errs = append(errs, err.Error())
	}

	// Validate Processing config.
	if c.Processing.MaxChunkSize <= 0 {
		errs = append(errs, "processing.max_chunk_size must be positive")
	}
	if c.Processing.MinChunkSize <= 0 {
		errs = append(errs, "processing.min_chunk_size must be positive")
	}
	if c.Processing.MinChunkSize > c.Processing.MaxChunkSize {
		errs = append(errs, "processing.min_chunk_size cannot be greater than max_chunk_size")
	}

	// Validate Paths config.
	if c.Paths.DataDir == "" {
		errs = append(errs, "paths.data_dir is required")
	}
	// TemplatesDir is intentionally optional: empty means "use
	// the templates embedded into the binary" (see
	// internal/web/templates.go). Operators can still set it
	// to ship a custom template set without rebuilding.

	if len(errs) > 0 {
		return fmt.Errorf("configuration validation failed:\n  - %s", strings.Join(errs, "\n  - "))
	}

	return nil
}
