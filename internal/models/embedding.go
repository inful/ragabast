package models

import (
	"time"
)

// Embedding represents a vector embedding for a chunk.
type Embedding struct {
	// ID is the unique identifier for the embedding.
	ID string `bson:"_id" json:"id"`

	// ChunkID is the ID of the chunk this embedding belongs to.
	ChunkID string `bson:"chunk_id" json:"chunk_id"`

	// DocumentID is the ID of the parent document.
	DocumentID string `bson:"document_id" json:"document_id"`

	// Model is the embedding model used (e.g., "nomic-embed-text-v1.5").
	Model string `bson:"model" json:"model"`

	// Vector is the actual embedding vector.
	Vector []float32 `bson:"vector" json:"vector"`

	// Dimension is the dimensionality of the vector.
	Dimension int `bson:"dimension" json:"dimension"`

	// Metadata.
	CreatedAt time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time `bson:"updated_at" json:"updated_at"`
}

// NewEmbedding creates a new embedding with default values.
func NewEmbedding() *Embedding {
	now := time.Now()
	return &Embedding{
		CreatedAt: now,
		UpdatedAt: now,
		Vector:    []float32{},
	}
}

// UpdateTimestamps updates the updated_at timestamp.
func (e *Embedding) UpdateTimestamps() {
	e.UpdatedAt = time.Now()
}

// IsValid checks if the embedding has valid data.
func (e *Embedding) IsValid() bool {
	return len(e.Vector) > 0 && e.Dimension > 0 && e.Model != ""
}

// CosineSimilarity calculates the cosine similarity between two embeddings.
func (e *Embedding) CosineSimilarity(other *Embedding) float32 {
	if len(e.Vector) != len(other.Vector) {
		return 0.0
	}

	var dotProduct, magnitudeE, magnitudeOther float32
	for i := range e.Vector {
		dotProduct += e.Vector[i] * other.Vector[i]
		magnitudeE += e.Vector[i] * e.Vector[i]
		magnitudeOther += other.Vector[i] * other.Vector[i]
	}

	if magnitudeE == 0 || magnitudeOther == 0 {
		return 0.0
	}

	return dotProduct / (magnitudeE * magnitudeOther)
}
