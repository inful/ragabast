package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
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

	// Vector database configuration.
	VectorDB VectorDBConfig `yaml:"vectordb"`

	// Server configuration.
	Server ServerConfig `yaml:"server"`

	// Processing configuration.
	Processing ProcessingConfig `yaml:"processing"`

	// Paths configuration.
	Paths PathsConfig `yaml:"paths"`
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

// VectorDBConfig holds vector database configuration.
type VectorDBConfig struct {
	// Persistence directory.
	PersistenceDir string `env:"VECTOR_DB_DIR" yaml:"persistence_dir"`

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

	// Enable CORS.
	EnableCORS bool `env:"SERVER_ENABLE_CORS" yaml:"enable_cors"`

	// ReadTimeout for HTTP requests.
	ReadTimeout time.Duration `env:"SERVER_READ_TIMEOUT" yaml:"read_timeout"`

	// WriteTimeout for HTTP responses.
	WriteTimeout time.Duration `env:"SERVER_WRITE_TIMEOUT" yaml:"write_timeout"`
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
		Ollama: OllamaConfig{
			BaseURL:        "http://localhost:11434",
			ChatBaseURL:    "http://localhost:11434",
			ChatModel:      "gemma:2b",
			EmbeddingModel: "nomic-embed-text:v1.5",
			Timeout:        30 * time.Second,
			// RAG-friendly defaults: low temperature + conservative sampling.
			Temperature: &defaultTemp,
			Options: map[string]any{
				"top_k":       20,
				"top_p":       0.8,
				"min_p":       0.05,
				"num_predict": 512,
			},
		},
		VectorDB: VectorDBConfig{
			PersistenceDir:     filepath.Join(wd, "data", "vectors"),
			CollectionName:     "ragabast",
			EmbeddingDimension: 768, // nomic-embed-text-v1.5 dimension
		},
		Server: ServerConfig{
			Address:      "0.0.0.0",
			Port:         8080,
			EnableCORS:   true,
			ReadTimeout:  15 * time.Second,
			WriteTimeout: 15 * time.Second,
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
func (c *Config) SaveConfig(path string) error {
	// Ensure directory exists.
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// ApplyEnvOverrides applies environment variable overrides to config fields.
func (c *Config) ApplyEnvOverrides() {
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

	// Paths config.
	if dir := os.Getenv("DATA_DIR"); dir != "" {
		c.Paths.DataDir = dir
	}
	if dir := os.Getenv("TEMPLATES_DIR"); dir != "" {
		c.Paths.TemplatesDir = dir
	}
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

// Validate checks if the configuration is valid.
func (c *Config) Validate() error {
	var errs []string

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
