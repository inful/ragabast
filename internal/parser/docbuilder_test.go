package parser

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseDocument_GeneratesFingerprintAndStableID(t *testing.T) {
	p := NewDocbuilderParser()

	raw := []byte("---\nfingerprint: \"auto-generated-if-empty\"\nuid: sample\nurls:\n  - https://example.com\n---\n\n# Title\nHello\n")

	doc, err := p.ParseDocument(raw, "test.md")
	require.NoError(t, err)
	require.Equal(t, "sample", doc.ID)
	require.Equal(t, "sample", doc.UID)
	require.NotEmpty(t, doc.Fingerprint)
	require.NotEmpty(t, doc.Fingerprint, "fingerprint should be populated even when not in frontmatter")
}

func TestParseDocument_PreservesExplicitFingerprintAndStableID(t *testing.T) {
	p := NewDocbuilderParser()

	fp := "b7add053acff6f4d1f5a7b6e66f7d6e6a8e2d9d8b1f956c534027be0f41fd3f9"
	raw := []byte("---\nfingerprint: " + fp + "\nuid: sample\nurls:\n  - https://example.com\n---\n\n# Title\nHello\n")

	doc, err := p.ParseDocument(raw, "test.md")
	require.NoError(t, err)
	require.Equal(t, "sample", doc.ID)
	require.Equal(t, "sample", doc.UID)
	require.Equal(t, fp, doc.Fingerprint)
}

func TestParseDocument_AllowsMissingURLs(t *testing.T) {
	p := NewDocbuilderParser()

	fp := "b7add053acff6f4d1f5a7b6e66f7d6e6a8e2d9d8b1f956c534027be0f41fd3f9"
	raw := []byte("---\nfingerprint: " + fp + "\nuid: sample\n---\n\n# Title\nHello\n")

	doc, err := p.ParseDocument(raw, "test.md")
	require.NoError(t, err)
	require.Equal(t, "sample", doc.ID)
	require.Empty(t, doc.URLs)
}
