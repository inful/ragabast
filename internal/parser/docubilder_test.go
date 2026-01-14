package parser

import (
	"testing"

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

	withoutURLs := []byte("---\nuid: sample\n---\n\n# Title\n")
	require.NoError(t, p.ValidateFrontmatter(withoutURLs))
}

func TestParseDocument_GeneratesFingerprintAndStableID(t *testing.T) {
	p := NewDocubilderParser()

	raw := []byte("---\nfingerprint: \"auto-generated-if-empty\"\nuid: sample\nurls:\n  - https://example.com\n---\n\n# Title\nHello\n")

	_, err := p.ParseDocument(raw, "test.md")
	require.Error(t, err)
}

func TestParseDocument_RequiresExplicitFingerprintAndStableID(t *testing.T) {
	p := NewDocubilderParser()

	fp := "b7add053acff6f4d1f5a7b6e66f7d6e6a8e2d9d8b1f956c534027be0f41fd3f9"
	raw := []byte("---\nfingerprint: " + fp + "\nuid: sample\nurls:\n  - https://example.com\n---\n\n# Title\nHello\n")

	doc, err := p.ParseDocument(raw, "test.md")
	require.NoError(t, err)
	require.Equal(t, "sample", doc.ID)
	require.Equal(t, "sample", doc.UID)
	require.Equal(t, fp, doc.Fingerprint)
}

func TestParseDocument_AllowsMissingURLs(t *testing.T) {
	p := NewDocubilderParser()

	fp := "b7add053acff6f4d1f5a7b6e66f7d6e6a8e2d9d8b1f956c534027be0f41fd3f9"
	raw := []byte("---\nfingerprint: " + fp + "\nuid: sample\n---\n\n# Title\nHello\n")

	doc, err := p.ParseDocument(raw, "test.md")
	require.NoError(t, err)
	require.Equal(t, "sample", doc.ID)
	require.Empty(t, doc.URLs)
}
