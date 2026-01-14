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

// OllamaConfig holds Ollama API configuration.
type OllamaConfig struct {
	// BaseURL is the Ollama API endpoint.
	BaseURL string `env:"OLLAMA_BASE_URL" yaml:"base_url"`

	// EmbeddingModel is the model used for embeddings.
	EmbeddingModel string `env:"OLLAMA_EMBEDDING_MODEL" yaml:"embedding_model"`

	// GenerationModel is the model used for text generation.
	GenerationModel string `env:"OLLAMA_GENERATION_MODEL" yaml:"generation_model"`

	// Temperature controls LLM sampling. If omitted, the model default is used.
	Temperature *float64 `env:"OLLAMA_TEMPERATURE" yaml:"temperature,omitempty"`

	// Options are model/runtime generation options passed to Ollama's /api/generate "options" object.
	// Common keys include: top_k, top_p, min_p, num_predict, num_ctx, seed, stop.
	Options map[string]any `env:"OLLAMA_OPTIONS_JSON" yaml:"options,omitempty"`

	// Timeout for API calls.
	Timeout time.Duration `env:"OLLAMA_TIMEOUT" yaml:"timeout"`

	// KeepAlive determines if connections should be kept alive.
	KeepAlive bool `env:"OLLAMA_KEEP_ALIVE" yaml:"keep_alive"`
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

	// MaxConcurrentProcessing is the maximum number of concurrent document processing operations.
	MaxConcurrentProcessing int `env:"PROCESSING_MAX_CONCURRENT" yaml:"max_concurrent_processing"`
}

// PathsConfig holds file path configuration.
type PathsConfig struct {
	// DataDir is the base directory for all data.
	DataDir string `env:"DATA_DIR" yaml:"data_dir"`

	// DocumentsDir is where ingested documents are stored.
	DocumentsDir string `env:"DOCUMENTS_DIR" yaml:"documents_dir"`

	// TempDir for temporary files.
	TempDir string `env:"TEMP_DIR" yaml:"temp_dir"`

	// TemplatesDir for web templates.
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
			BaseURL:         "http://localhost:11434",
			EmbeddingModel:  "nomic-embed-text:v1.5",
			GenerationModel: "gemma:2b",
			Timeout:         30 * time.Second,
			KeepAlive:       true,
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
			MaxChunkSize:            2000,
			MinChunkSize:            300,
			ChunkOverlap:            150,
			MaxConcurrentProcessing: 5,
		},
		Paths: PathsConfig{
			DataDir:      filepath.Join(wd, "data"),
			DocumentsDir: filepath.Join(wd, "data", "documents"),
			TempDir:      filepath.Join(wd, "data", "temp"),
			TemplatesDir: filepath.Join(wd, "templates"),
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
	if model := os.Getenv("OLLAMA_GENERATION_MODEL"); model != "" {
		c.Ollama.GenerationModel = model
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
	if dir := os.Getenv("DOCUMENTS_DIR"); dir != "" {
		c.Paths.DocumentsDir = dir
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
	if c.Ollama.GenerationModel == "" {
		errs = append(errs, "ollama.generation_model is required")
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
	if c.Paths.DocumentsDir == "" {
		errs = append(errs, "paths.documents_dir is required")
	}
	if c.Paths.TemplatesDir == "" {
		errs = append(errs, "paths.templates_dir is required")
	}

	if len(errs) > 0 {
		return fmt.Errorf("configuration validation failed:\n  - %s", strings.Join(errs, "\n  - "))
	}

	return nil
}

// EnsureDirectories creates all necessary directories for the configuration.
func (c *Config) EnsureDirectories() error {
	dirs := []string{
		c.Paths.DataDir,
		c.Paths.DocumentsDir,
		c.Paths.TempDir,
		c.VectorDB.PersistenceDir,
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	return nil
}

// GetServerAddress returns the full server address.
func (c *Config) GetServerAddress() string {
	return c.Server.ListenAddr()
}

// GetOllamaEmbeddingURL returns the full URL for embedding requests.
func (c *Config) GetOllamaEmbeddingURL() string {
	return fmt.Sprintf("%s/api/embeddings", strings.TrimRight(c.Ollama.BaseURL, "/"))
}

// GetOllamaGenerateURL returns the full URL for generation requests.
func (c *Config) GetOllamaGenerateURL() string {
	return fmt.Sprintf("%s/api/generate", strings.TrimRight(c.Ollama.BaseURL, "/"))
}

// GetOllamaTagsURL returns the full URL for model listing.
func (c *Config) GetOllamaTagsURL() string {
	return fmt.Sprintf("%s/api/tags", strings.TrimRight(c.Ollama.BaseURL, "/"))
}
