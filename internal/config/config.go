package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

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
