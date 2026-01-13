package parser

import (
	"crypto/sha256"
	"encoding/hex"
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
}

func TestParseDocument_GeneratesFingerprintAndStableID(t *testing.T) {
	p := NewDocubilderParser()

	raw := []byte("---\nfingerprint: \"auto-generated-if-empty\"\nuid: sample\nurls:\n  - https://example.com\n---\n\n# Title\nHello\n")

	doc, err := p.ParseDocument(raw, "test.md")
	require.NoError(t, err)
	require.NotEmpty(t, doc.Content)

	h := sha256.Sum256([]byte(doc.Content))
	expected := hex.EncodeToString(h[:])

	require.Equal(t, expected, doc.Fingerprint)
	require.Equal(t, expected, doc.ID)
	require.NotEqual(t, "auto-generated-if-empty", doc.Fingerprint)
}
