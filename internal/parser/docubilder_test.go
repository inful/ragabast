package parser

import (
	"testing"

	"github.com/inful/mdfp"
	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/require"
)

func TestValidateFrontmatter(t *testing.T) {
	p := NewDocubilderParser()

	noFrontmatter := []byte("# Title\n\nBody")
	err := p.ValidateFrontmatter(noFrontmatter)
	require.ErrorIs(t, err, models.ErrInvalidFrontmatter)

	withFrontmatter := []byte("---\nuid: sample\nurls:\n  - https://example.com\n---\n\n# Title\n")
	require.NoError(t, p.ValidateFrontmatter(withFrontmatter))
}

func TestParseDocument_GeneratesFingerprintAndStableID(t *testing.T) {
	p := NewDocubilderParser()

	raw := []byte("---\nfingerprint: \"auto-generated-if-empty\"\nuid: sample\nurls:\n  - https://example.com\n---\n\n# Title\nHello\n")

	doc, err := p.ParseDocument(raw, "test.md")
	require.NoError(t, err)
	require.NotEmpty(t, doc.Content)

	expected := mdfp.CalculateFingerprint(doc.Content)

	require.Equal(t, expected, doc.Fingerprint)
	require.Equal(t, "sample", doc.ID)
	require.Equal(t, "sample", doc.UID)
	require.NotEqual(t, "auto-generated-if-empty", doc.Fingerprint)
}
