package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
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

	// DefaultProviderName, when set, restricts the auth
	// surface to a single provider: the chooser page is
	// never rendered, /auth/login auto-redirects to the
	// default, and requests to non-default provider URLs
	// 404. Useful for multi-tenant deployments where
	// each instance is pinned to one IdP, or when an
	// operator wants to enforce "all sign-ins use this
	// provider" without exposing the others.
	//
	// Lives at the AuthConfig level (not on each provider)
	// because it's a property of the deployment, not of
	// the providers themselves — a single IdP can be
	// configured on many instances, but only some of
	// those instances may pin to it.
	//
	// Must match the `name:` field of one entry in
	// Providers (case-sensitive). Empty = no default;
	// multi-provider installs show the chooser as
	// before. Validate() surfaces "default set but no
	// matching provider" as a startup error so the
	// misconfiguration doesn't silently fall through to
	// the chooser.
	DefaultProviderName string `env:"AUTH_DEFAULT_PROVIDER_NAME" yaml:"default_provider_name,omitempty"`

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
//     that returns a stable user id and a verified email.
//   - gitlab:  read_user + profile + email — read_user
//     is the GitLab API scope that grants /api/v4/user
//     access. Without it, GitLab returns HTTP 403 on
//     the userinfo fetch regardless of token validity.
//     profile and email are OIDC claims GitLab populates
//     on the /oauth/userinfo endpoint when read_user is
//     granted. openid is intentionally NOT in the default
//     set — it's only honored on GitLab 16.0+ with OIDC
//     applications explicitly enabled, and on older GitLabs
//     requesting it produces a confusing login failure.
//     Operators on OIDC-enabled GitLab who want the
//     ID-token path should switch to type=oidc with a
//     discovery_url instead.
//   - forgejo: read:user + user:email — matches the
//     GitHub-compatible Forgejo OAuth shape.
//   - oidc:    openid + profile + email — required by
//     the OIDC spec for ID-token issuance. type=oidc
//     providers always go through the OIDC UserInfo
//     claim flow, so these are sufficient.
func (p OAuthProvider) EffectiveScopes() []string {
	if len(p.Scopes) > 0 {
		out := make([]string, len(p.Scopes))
		copy(out, p.Scopes)

		return out
	}
	switch p.Type {
	case "github", "forgejo":
		return []string{"read:user", "user:email"}
	case "gitlab":
		// read_user is the GitLab API scope that
		// unlocks /api/v4/user. profile and email
		// are the OIDC claims GitLab populates
		// alongside it. openid is omitted here on
		// purpose — see the docstring above.
		return []string{"read_user", "profile", "email"}
	case "oidc":
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
			// RAG-friendly defaults: low temperature + conservative
			// sampling. The default Options map contains only params that
			// are universal across the OpenAI-compat ecosystem — `top_p`
			// is in the OpenAI chat completions spec; `top_k` and
			// `num_predict` are vendor extensions (Ollama / llama.cpp /
			// vLLM) but supported by every server ragabast targets.
			//
			// `min_p` is NOT in the defaults even though it's a
			// reasonable sampling knob — it's not in OpenAI's spec and
			// vLLM rejects it (with HTTP 400) when speculative
			// decoding is enabled, even on operators who want it.
			// Operators running Ollama / llama.cpp / non-spec-decoding
			// vLLM can opt back in via:
			//   - YAML:   ollama.options.min_p: 0.05
			//   - env:    OLLAMA_OPTIONS_JSON='{"min_p": 0.05, ...}'
			// The validateOllamaOptions range check still catches
			// out-of-range opt-ins.
			Temperature: &defaultTemp,
			Options: map[string]any{
				"top_k":       20,
				"top_p":       0.8,
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
			Address:                "0.0.0.0",
			Port:                   8080,
			EnableCORS:             true,
			MaxIngestDocumentBytes: 1 << 20, // 1 MiB
			ReadTimeout:            15 * time.Second,
			// WriteTimeout must be at least the slowest LLM
			// timeout plus a buffer; see Config.Validate. The
			// historical 15s default silently truncated chat
			// responses when the LLM took longer than 15s
			// (issue: second chat message did not render).
			// 60s comfortably covers the default 30s LLM
			// timeout and gives headroom for chat streaming.
			WriteTimeout:               60 * time.Second,
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
	// Capture the cwd-relative default for VectorDB.PersistenceDir
	// so the DATA_DIR re-derivation step at the end of this
	// method can tell the operator's explicit VECTOR_DB_DIR
	// override apart from the DefaultConfig() default.
	// See the long comment on the derivation block below for
	// the deployment bug this gates against.
	defaultPersistenceDir := c.VectorDB.PersistenceDir

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
	if dir := os.Getenv("VECTOR_DB_KEYWORD_INDEX_DIR"); dir != "" {
		c.VectorDB.KeywordIndexDir = dir
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
	if v := os.Getenv("AUTH_DEFAULT_PROVIDER_NAME"); v != "" {
		c.Auth.DefaultProviderName = v
	}

	// Tilde expansion for path fields.
	//
	// resolvePath() expands a leading "~/" or "~" to the user's
	// home directory. We already apply it to the config FILE
	// path inside LoadConfig (the argument to --config /
	// LoadConfig) so `ragabast --config ~/.ragabast.yml`
	// works without shell tilde expansion. The path FIELDS
	// inside the config (paths.data_dir,
	// paths.templates_dir, vectordb.persistence_dir,
	// vectordb.keyword_index_dir,
	// server.async_ingest_queue_dir) used to be stored
	// verbatim regardless of source, so `data_dir: ~/data` in
	// YAML or `DATA_DIR=~/data` in the environment produced a
	// literal "~/data" string that failed downstream the
	// moment the OS tried to open it.
	//
	// We normalize every path field here, after YAML + env
	// merge, so:
	//
	//   - YAML values like `data_dir: ~/data` get expanded.
	//   - Env-var values like `DATA_DIR=~/data` get expanded
	//     the same way (ApplyEnvOverrides reads the env
	//     directly, then this block normalizes).
	//   - Absolute paths (`/var/lib/ragabast`) and relative
	//     paths (`data/sub`) pass through unchanged.
	//   - Empty strings stay empty — empty is the "use the
	//     default" signal for every path field and must not
	//     silently turn into the user's home directory.
	//
	// resolvePath itself is safe-by-default: if HOME /
	// USERPROFILE lookup fails for any reason, it returns the
	// input unchanged, so a missing home dir surfaces as a
	// downstream "no such file" error rather than a mangled
	// path.
	c.Paths.DataDir = resolvePath(c.Paths.DataDir)
	c.Paths.TemplatesDir = resolvePath(c.Paths.TemplatesDir)
	c.VectorDB.PersistenceDir = resolvePath(c.VectorDB.PersistenceDir)
	c.VectorDB.KeywordIndexDir = resolvePath(c.VectorDB.KeywordIndexDir)
	c.Server.AsyncIngestQueueDir = resolvePath(c.Server.AsyncIngestQueueDir)

	// DATA_DIR re-derivation for vectordb.persistence_dir.
	//
	// Why: DefaultConfig sets VectorDB.PersistenceDir to
	// <filepath>.Join(wd, "data", "vectors")</filepath>. That
	// default is cwd-relative. On a workstation install where
	// the operator runs `./ragabast serve` from a project
	// directory, the default lands at <project>/data/vectors
	// — fine. On a containerized install, the published
	// distroless image has WORKDIR=/home/nonroot (the
	// `nonroot` variant), so the default lands at
	// /home/nonroot/data/vectors — outside any PVC the
	// operator mounted at /data. The operator's mental
	// model is "DATA_DIR is where data goes", so they set
	// DATA_DIR=/data and the PVC to /data, but the vector DB
	// still ends up in /home/nonroot/data/vectors and is
	// wiped on every pod restart.
	//
	// The fix: when DATA_DIR is set (env or YAML) and the
	// persistence_dir field is still at the cwd-relative
	// default (the equality check against defaultPersistenceDir
	// captured at the top of this method), re-derive it to
	// <DataDir>/vectors. An explicit VECTOR_DB_DIR override
	// moves PersistenceDir away from the default, fails the
	// equality check, and is preserved verbatim — the
	// operator's explicit choice always wins.
	//
	// This is the deployment-footgun fix; TestApplyEnvOverrides_DataDirMovesPersistenceDir
	// and TestApplyEnvOverrides_ExplicitVECTOR_DB_DIR_BeatsDerivation
	// pin both halves of the contract.
	if c.Paths.DataDir != "" && c.VectorDB.PersistenceDir == defaultPersistenceDir {
		c.VectorDB.PersistenceDir = filepath.Join(c.Paths.DataDir, "vectors")
	}
}

// validateAuth runs the cross-provider validation: each
// provider validates itself, then we check for duplicate
// names, reserved-name collisions, and — when
// DefaultProviderName is set — that the default matches a
// configured provider. Returned error is a single combined
// message suitable for Validate()'s errs list (joined with
// newlines).
func (c *Config) validateAuth() error {
	if len(c.Auth.Providers) == 0 {
		if c.Auth.DefaultProviderName != "" {
			return fmt.Errorf("auth.default_provider_name %q is set but no providers are configured", c.Auth.DefaultProviderName)
		}

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

	// Validate the default-provider pin AFTER the per-provider
	// checks so we don't report a "default doesn't match" error
	// for a config that has a more fundamental problem.
	if c.Auth.DefaultProviderName != "" {
		if !seen[c.Auth.DefaultProviderName] {
			errs = append(errs, fmt.Sprintf(
				"auth.default_provider_name %q does not match any configured provider (available: %s)",
				c.Auth.DefaultProviderName,
				availableProviderNames(c.Auth.Providers),
			))
		}
	}

	if len(errs) == 0 {
		return nil
	}

	return fmt.Errorf("%s", strings.Join(errs, "; "))
}

// availableProviderNames returns a sorted, quoted list of
// provider names. Used to make "default_provider_name points
// at nothing" errors actionable — the operator sees the
// actual valid options in the error message rather than
// having to scroll back through their config to remember
// what they named everything.
func availableProviderNames(providers []OAuthProvider) string {
	names := make([]string, 0, len(providers))
	for _, p := range providers {
		names = append(names, strconv.Quote(p.Name))
	}

	return strings.Join(names, ", ")
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

	// Validate that WriteTimeout can fit the slowest LLM call
	// the operator has configured. A chat response streams the
	// LLM completion through the HTTP body, so a WriteTimeout
	// shorter than the LLM timeout causes the server to close
	// the connection mid-response — the operator sees the first
	// message render and the second message silently fail to
	// swap. We pin WriteTimeout >= max(Ollama.Timeout,
	// EmbeddingGemma.Timeout) plus a small buffer for the
	// surrounding template + htmx swap work.
	//
	// This was a v0.11.0 regression that surfaced as "second
	// chat message does nothing": the previous defaults left
	// the per-call timeout unbounded but server.WriteTimeout
	// capped responses at 15s, so a slow LLM silently
	// truncated the response. Validate catches the
	// misconfiguration at startup instead of in production.
	if c.Server.WriteTimeout > 0 {
		// Compute the longest per-call timeout the operator
		// has configured across both providers.
		var maxLLMTimeout time.Duration
		if c.Ollama.Timeout > maxLLMTimeout {
			maxLLMTimeout = c.Ollama.Timeout
		}
		if c.EmbeddingGemma.Timeout > maxLLMTimeout {
			maxLLMTimeout = c.EmbeddingGemma.Timeout
		}
		// 5s buffer covers the chat template render and
		// htmx-swap-friendly framing after the LLM call
		// returns.
		const writeBuffer = 5 * time.Second
		required := maxLLMTimeout + writeBuffer
		if c.Server.WriteTimeout < required {
			errs = append(errs, fmt.Sprintf(
				"server.write_timeout (%s) is shorter than the longest LLM timeout (%s) plus a 5s buffer; raise it to at least %s so chat responses don't get truncated mid-stream",
				c.Server.WriteTimeout, maxLLMTimeout, required,
			))
		}
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
