package models

import (
	"slices"
	"time"

	"github.com/google/uuid"
)

// Document represents a docubilder document with YAML frontmatter.
type Document struct {
	// ID is the unique identifier for the document.
	ID string `bson:"_id" json:"id"`

	// Fingerprint is a unique hash of the document content.
	Fingerprint string `bson:"fingerprint" json:"fingerprint"`

	// UID is a user-defined unique identifier.
	UID string `bson:"uid" json:"uid"`

	// Tags are user-defined labels for categorization.
	Tags []string `bson:"tags" json:"tags"`

	// Categories are hierarchical classifications.
	Categories []string `bson:"categories" json:"categories"`

	// URLs associated with the document.
	URLs []string `bson:"urls" json:"urls"`

	// Title is extracted from the first H1 header.
	Title string `bson:"title" json:"title"`

	// Content is the full markdown content.
	Content string `bson:"content" json:"content"`

	// RawContent is the original file content including frontmatter.
	RawContent []byte `bson:"raw_content" json:"-"`

	// Chunks are the processed document chunks.
	Chunks []Chunk `bson:"chunks" json:"chunks"`

	// Metadata.
	CreatedAt time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time `bson:"updated_at" json:"updated_at"`
	FilePath  string    `bson:"file_path" json:"file_path"`
}

// NewDocument creates a new document with default values.
func NewDocument() *Document {
	now := time.Now()
	return &Document{
		ID:         uuid.New().String(),
		CreatedAt:  now,
		UpdatedAt:  now,
		Tags:       []string{},
		Categories: []string{},
		URLs:       []string{},
		Chunks:     []Chunk{},
	}
}

// Validate checks if the document has required fields.
func (d *Document) Validate() error {
	if d.Fingerprint == "" {
		return ErrMissingFingerprint
	}
	if d.UID == "" {
		return ErrMissingUID
	}
	if d.Content == "" {
		return ErrMissingContent
	}
	return nil
}

// AddTag adds a tag to the document if not already present.
func (d *Document) AddTag(tag string) {
	if slices.Contains(d.Tags, tag) {
		return
	}
	d.Tags = append(d.Tags, tag)
}

// AddCategory adds a category to the document if not already present.
func (d *Document) AddCategory(category string) {
	if slices.Contains(d.Categories, category) {
		return
	}
	d.Categories = append(d.Categories, category)
}

// AddURL adds a URL to the document if not already present.
func (d *Document) AddURL(url string) {
	if slices.Contains(d.URLs, url) {
		return
	}
	d.URLs = append(d.URLs, url)
}

// UpdateTimestamps updates the updated_at timestamp.
func (d *Document) UpdateTimestamps() {
	d.UpdatedAt = time.Now()
}

// HeaderInfo represents a header extracted from the document content.
type HeaderInfo struct {
	Level int    `json:"level"`
	Text  string `json:"text"`
	Line  int    `json:"line"`
}

// DocumentInfo represents basic information about a document.
type DocumentInfo struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Fingerprint string    `json:"fingerprint"`
	UID         string    `json:"uid"`
	Tags        []string  `json:"tags"`
	Categories  []string  `json:"categories"`
	URLs        []string  `json:"urls"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	ChunkCount  int       `json:"chunk_count"`
}
