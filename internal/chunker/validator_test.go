package chunker

import (
	"testing"

	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/require"
)

func TestValidateDocument_AllowsMissingURLs(t *testing.T) {
	doc := models.NewDocument()
	doc.UID = "uid"
	doc.ID = "uid"
	doc.Fingerprint = "fp"
	doc.Content = "# Title\nBody"
	doc.URLs = nil

	require.NoError(t, ValidateDocument(doc))
}

func TestValidateDocument_RejectsInvalidURLWhenPresent(t *testing.T) {
	doc := models.NewDocument()
	doc.UID = "uid"
	doc.ID = "uid"
	doc.Fingerprint = "fp"
	doc.Content = "# Title\nBody"
	doc.URLs = []string{"not a url"}

	err := ValidateDocument(doc)
	require.Error(t, err)
	require.ErrorIs(t, err, models.ErrInvalidFormat)
}
