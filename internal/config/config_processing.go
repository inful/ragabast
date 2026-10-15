package config

// ProcessingConfig holds document processing configuration.
type ProcessingConfig struct {
	// MaxChunkSize is the maximum size of a chunk in characters.
	MaxChunkSize int `env:"PROCESSING_MAX_CHUNK_SIZE" yaml:"max_chunk_size"`

	// MinChunkSize is the minimum size of a chunk in characters.
	MinChunkSize int `env:"PROCESSING_MIN_CHUNK_SIZE" yaml:"min_chunk_size"`

	// ChunkOverlap is the number of characters to overlap between chunks.
	ChunkOverlap int `env:"PROCESSING_CHUNK_OVERLAP" yaml:"chunk_overlap"`
}
