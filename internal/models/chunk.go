package models

import (
	"slices"
	"time"
)

// Chunk represents a portion of a document with hierarchical context.
type Chunk struct {
	// ID is the unique identifier for the chunk.
	ID string `bson:"_id" json:"id"`

	// DocumentID is the ID of the parent document.
	DocumentID string `bson:"document_id" json:"document_id"`

	// Content is the text content of this chunk.
	Content string `bson:"content" json:"content"`

	// HeaderPath represents the hierarchical path (e.g., "H1 > H2").
	HeaderPath string `bson:"header_path" json:"header_path"`

	// Level is the header level (1 for H1, 2 for H2, etc.).
	Level int `bson:"level" json:"level"`

	// ParentID is the ID of the parent chunk (if any).
	ParentID string `bson:"parent_id" json:"parent_id"`

	// ChildrenIDs are IDs of child chunks.
	ChildrenIDs []string `bson:"children_ids" json:"children_ids"`

	// StartLine is the starting line in the original document.
	StartLine int `bson:"start_line" json:"start_line"`

	// EndLine is the ending line in the original document.
	EndLine int `bson:"end_line" json:"end_line"`

	// Metadata for the chunk.
	CreatedAt time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time `bson:"updated_at" json:"updated_at"`

	// EmbeddingID references the vector embedding for this chunk.
	EmbeddingID string `bson:"embedding_id" json:"embedding_id"`

	// DocumentTitle is the title of the parent document.
	DocumentTitle string `bson:"document_title" json:"document_title"`

	// Fingerprint is a unique hash of the document content.
	Fingerprint string `bson:"fingerprint" json:"fingerprint"`

	// UID is a user-defined unique identifier.
	UID string `bson:"uid" json:"uid"`

	// DocumentURLs are the source URLs associated with the parent document.
	DocumentURLs []string `bson:"document_urls" json:"document_urls"`

	// DocumentTags are user-defined labels for the parent document.
	DocumentTags []string `bson:"document_tags" json:"document_tags"`

	// DocumentCategories are hierarchical classifications for the parent document.
	DocumentCategories []string `bson:"document_categories" json:"document_categories"`

	// DocumentCreatedAt is the parent document creation time.
	DocumentCreatedAt time.Time `bson:"document_created_at" json:"document_created_at"`

	// DocumentUpdatedAt is the parent document update time.
	DocumentUpdatedAt time.Time `bson:"document_updated_at" json:"document_updated_at"`
}

// NewChunk creates a new chunk with default values.
func NewChunk() *Chunk {
	now := time.Now()
	return &Chunk{
		ID:          "", // Will be set by processor
		ChildrenIDs: []string{},
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

// AddChild adds a child chunk ID to this chunk.
func (c *Chunk) AddChild(childID string) {
	if slices.Contains(c.ChildrenIDs, childID) {
		return
	}
	c.ChildrenIDs = append(c.ChildrenIDs, childID)
}

// UpdateTimestamps updates the updated_at timestamp.
func (c *Chunk) UpdateTimestamps() {
	c.UpdatedAt = time.Now()
}

// GetFullPath returns the full hierarchical path including this chunk's content.
func (c *Chunk) GetFullPath() string {
	if c.HeaderPath == "" {
		return c.Content
	}
	return c.HeaderPath + " > " + c.Content
}

// HierarchicalInfo represents the hierarchical structure of document chunks.
type HierarchicalInfo struct {
	RootChunks  []string            `json:"root_chunks"`
	ChildMap    map[string][]string `json:"child_map"`
	LevelMap    map[int][]string    `json:"level_map"`
	HeaderPaths map[string]string   `json:"header_paths"`
}

// TreeNode represents a node in the document tree structure.
type TreeNode struct {
	ChunkID  string      `json:"chunk_id"`
	Content  string      `json:"content"`
	Level    int         `json:"level"`
	Children []*TreeNode `json:"children"`
}

// DocumentTree represents the complete tree structure of a document.
type DocumentTree struct {
	Root  *TreeNode            `json:"root"`
	Nodes map[string]*TreeNode `json:"nodes"`
	Level int                  `json:"level"`
}

// SearchResult represents a result from vector similarity search.
type SearchResult struct {
	ChunkID       string   `json:"chunk_id"`
	DocumentID    string   `json:"document_id"`
	Content       string   `json:"content"`
	HeaderPath    string   `json:"header_path"`
	Level         int      `json:"level"`
	StartLine     int      `json:"start_line"`
	EndLine       int      `json:"end_line"`
	DocumentTitle string   `json:"document_title"`
	DocumentURLs  []string `json:"document_urls,omitempty"`
	Similarity    float32  `json:"similarity"`
	Fingerprint   string   `json:"fingerprint,omitempty"`
	UID           string   `json:"uid,omitempty"`
}
