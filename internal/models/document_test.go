package models

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewDocument_DefaultsAreValid(t *testing.T) {
	doc := NewDocument()

	require.NotEmpty(t, doc.ID, "ID should be a generated UUID")
	require.False(t, doc.CreatedAt.IsZero(), "CreatedAt should default to now")
	require.False(t, doc.UpdatedAt.IsZero(), "UpdatedAt should default to now")
	require.NotNil(t, doc.Tags, "Tags should be a non-nil empty slice so JSON encodes as []")
	require.NotNil(t, doc.Categories)
	require.NotNil(t, doc.URLs)
	require.NotNil(t, doc.Chunks)
}

func TestDocument_Validate_RequiresFingerprint(t *testing.T) {
	doc := &Document{UID: "u", Content: "c"}

	err := doc.Validate()
	require.ErrorIs(t, err, ErrMissingFingerprint)
}

func TestDocument_Validate_RequiresUID(t *testing.T) {
	doc := &Document{Fingerprint: "f", Content: "c"}

	err := doc.Validate()
	require.ErrorIs(t, err, ErrMissingUID)
}

func TestDocument_Validate_RequiresContent(t *testing.T) {
	doc := &Document{Fingerprint: "f", UID: "u"}

	err := doc.Validate()
	require.ErrorIs(t, err, ErrMissingContent)
}

func TestDocument_Validate_HappyPath(t *testing.T) {
	doc := &Document{Fingerprint: "f", UID: "u", Content: "c"}

	require.NoError(t, doc.Validate())
}

// TestDocument_Validate_ReturnsFirstMissingField pins the order
// of validation. The first missing required field is the one
// returned; if a caller is missing two, they fix one and rerun.
func TestDocument_Validate_ReturnsFirstMissingField(t *testing.T) {
	doc := &Document{}

	err := doc.Validate()
	require.ErrorIs(t, err, ErrMissingFingerprint)

	doc.Fingerprint = "f"
	err = doc.Validate()
	require.ErrorIs(t, err, ErrMissingUID)

	doc.UID = "u"
	err = doc.Validate()
	require.ErrorIs(t, err, ErrMissingContent)

	doc.Content = "c"
	require.NoError(t, doc.Validate())
}

func TestDocument_AddTag_Dedupes(t *testing.T) {
	doc := NewDocument()

	doc.AddTag("go")
	doc.AddTag("go")
	doc.AddTag("rag")
	doc.AddTag("go")

	require.Equal(t, []string{"go", "rag"}, doc.Tags,
		"first occurrence wins; subsequent identical tags are ignored")
}

func TestDocument_AddTag_HandlesNilTagsSlice(t *testing.T) {
	doc := &Document{} // Tags is nil until NewDocument is called

	require.NotPanics(t, func() {
		doc.AddTag("go")
	})
	require.Equal(t, []string{"go"}, doc.Tags)
}

func TestDocument_AddCategory_Dedupes(t *testing.T) {
	doc := NewDocument()

	doc.AddCategory("Guides")
	doc.AddCategory("Reference")
	doc.AddCategory("Guides")

	require.Equal(t, []string{"Guides", "Reference"}, doc.Categories,
		"case-sensitive: 'Guides' and 'guides' are different")
}

func TestDocument_AddURL_Dedupes(t *testing.T) {
	doc := NewDocument()

	doc.AddURL("https://a.example")
	doc.AddURL("https://a.example")
	doc.AddURL("https://b.example")

	require.Equal(t, []string{"https://a.example", "https://b.example"}, doc.URLs)
}

// TestDocument_UpdateTimestamps_BumpsUpdatedAtOnly ensures
// UpdateTimestamps does not touch CreatedAt; that would silently
// break ingest timestamps.
func TestDocument_UpdateTimestamps_BumpsUpdatedAtOnly(t *testing.T) {
	doc := NewDocument()
	originalCreated := doc.CreatedAt

	doc.UpdateTimestamps()

	require.True(t, doc.UpdatedAt.After(originalCreated) || doc.UpdatedAt.Equal(originalCreated),
		"UpdatedAt should be at or after CreatedAt")
	require.True(t, doc.CreatedAt.Equal(originalCreated),
		"CreatedAt must not change on update")
}

// TestErrors_AreDistinct guards against future refactors that
// might accidentally merge these sentinels (e.g. wrapping one
// inside another). Callers rely on errors.Is to discriminate.
func TestErrors_AreDistinct(t *testing.T) {
	sentinels := []error{
		ErrMissingFingerprint,
		ErrMissingUID,
		ErrMissingURL,
		ErrMissingContent,
		ErrInvalidFrontmatter,
		ErrParseFailed,
		ErrDocumentNotFound,
		ErrChunkNotFound,
		ErrSearchFailed,
		ErrNotFound,
		ErrEmbeddingFailed,
		ErrGenerationFailed,
		ErrOllamaNotRunning,
		ErrModelNotFound,
		ErrInvalidChunkSize,
		ErrEmptyDocument,
		ErrInvalidFormat,
		ErrInvalidInput,
	}
	for i, a := range sentinels {
		for j, b := range sentinels {
			if i == j {
				continue
			}
			require.NotErrorIs(t, a, b,
				"%v must not match %v", a, b)
		}
	}
}
