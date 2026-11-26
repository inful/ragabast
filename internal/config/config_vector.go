package config

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

	// FailOnModelMismatch decides what happens when the store on
	// disk was written by a different embedding model than the
	// one now configured.
	//
	// false (the default): ragabast invalidates the stale corpus
	// by moving the store aside (see the QuarantineStore helper)
	// and starting fresh — the vectors are unusable under a new
	// embedder, and the source documents can be re-embedded. The
	// stale copy stays on disk until the operator deletes it.
	//
	// true: the server refuses to start and names
	// `ragabast vector reset --force` as the fix. Use this when
	// you would rather stop and look before anything moves.
	FailOnModelMismatch bool `env:"VECTOR_DB_FAIL_ON_MODEL_MISMATCH" yaml:"fail_on_model_mismatch"`
}
