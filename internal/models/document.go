package models

import (
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Document represents a docbuilder document with YAML frontmatter.
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

	// Title is set from the frontmatter `title:` field by the
	// parser. There is no H1 fallback — a document that doesn't
	// declare `title:` has an empty Title, and DisplayLabel fills the
	// gap with the parent document's basename or its UID.
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

	// Draft mirrors the frontmatter `draft:` flag from Hugo.
	// When true, the document is filtered out of the embeddings
	// at parse time (see parser.IsUnpublished) so the database
	// never holds a document Hugo would not have published.
	Draft bool `bson:"draft" json:"draft"`

	// Date mirrors Hugo's frontmatter `date:` field. The parser
	// populates this from RFC 3339; unparseable inputs leave it
	// at the zero value. Used only by IsUnpublished — not part of
	// the search surface.
	Date time.Time `bson:"date" json:"date"`

	// PublishDate mirrors Hugo's frontmatter `publishDate:`
	// field (with `pubdate` and `published` aliases honored by
	// the parser). Used only by IsUnpublished.
	PublishDate time.Time `bson:"publish_date" json:"publish_date"`

	// ExpiryDate mirrors Hugo's frontmatter `expiryDate:` field
	// (with `unpublishdate` alias honored by the parser). Used
	// only by IsUnpublished.
	ExpiryDate time.Time `bson:"expiry_date" json:"expiry_date"`

	// SourceKind identifies where the document came from
	// (docbuilder, GitLab, etc.). Defaults to SourceUnknown for
	// documents ingested before the field was added; downstream
	// code treats that as SourceDocbuilder per the spec.
	SourceKind SourceKind `bson:"source_kind,omitempty" json:"source_kind,omitempty"`
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
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Fingerprint string   `json:"fingerprint"`
	UID         string   `json:"uid"`
	Tags        []string `json:"tags"`
	Categories  []string `json:"categories"`
	URLs        []string `json:"urls"`
	// FilePath is the on-disk path the parent document was
	// ingested from, mirrored from the chunk metadata so the
	// /documents page can fall back to the filename when a
	// document has no title. Empty for documents ingested
	// before this field was added.
	FilePath string `json:"file_path,omitempty"`
	// DocbuilderURL is the synthetic permalink derived from
	// ragabast.docbuilder_base_url + UID, populated by the
	// service layer. Empty when the base URL is not configured.
	DocbuilderURL string    `json:"docbuilder_url,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	ChunkCount    int       `json:"chunk_count"`
}

// DisplayLabel returns the human-readable label for the document.
// The preference order mirrors SearchResult.DisplayLabel:
//
//  1. Title (set from the parent document's frontmatter
//     `title:` field by the parser — there is no H1 fallback)
//  2. FilenameFromPath(FilePath) (the basename without its
//     extension — the on-disk filename the operator recognizes)
//  3. UID (the existing fallback, the user-defined unique id)
//
// Sharing the same fallback chain across chat sources,
// /search results, and /documents means a future change to the
// label logic only has to be made in one place.
func (d DocumentInfo) DisplayLabel() string {
	if t := strings.TrimSpace(d.Title); t != "" {
		return t
	}
	if path := strings.TrimSpace(d.FilePath); path != "" {
		if name := FilenameFromPath(path); name != "" {
			return name
		}
	}
	return d.UID
}
