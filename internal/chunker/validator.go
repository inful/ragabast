package chunker

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/ragabast/internal/models"
)

// ValidateChunk validates a chunk for completeness and correctness.
func ValidateChunk(chunk *models.Chunk) error {
	if chunk == nil {
		return models.ErrInvalidChunkSize
	}

	if strings.TrimSpace(chunk.Content) == "" {
		return fmt.Errorf("%w: chunk content cannot be empty", models.ErrInvalidChunkSize)
	}

	if chunk.StartLine > chunk.EndLine {
		return fmt.Errorf("%w: start line cannot be greater than end line", models.ErrInvalidChunkSize)
	}

	if chunk.Level < 0 {
		return fmt.Errorf("%w: level cannot be negative", models.ErrInvalidChunkSize)
	}

	return nil
}

// ValidateDocument validates a document for required fields.
func ValidateDocument(doc *models.Document) error {
	if doc == nil {
		return models.ErrInvalidFormat
	}

	// Validate fingerprint
	if strings.TrimSpace(doc.Fingerprint) == "" {
		return fmt.Errorf("%w: fingerprint is required", models.ErrMissingFingerprint)
	}

	// Validate UID
	if strings.TrimSpace(doc.UID) == "" {
		return fmt.Errorf("%w: UID is required", models.ErrMissingUID)
	}

	// Validate URLs (optional)
	for _, urlStr := range doc.URLs {
		if _, err := url.ParseRequestURI(urlStr); err != nil {
			return fmt.Errorf("%w: invalid URL '%s': %w", models.ErrInvalidFormat, urlStr, err)
		}
	}

	// Validate content
	if strings.TrimSpace(doc.Content) == "" {
		return fmt.Errorf("%w: document content cannot be empty", models.ErrMissingContent)
	}

	return nil
}

// ValidateFrontmatter validates the frontmatter fields.
func ValidateFrontmatter(fingerprint, uid string, tags, categories []string, urls []string) error {
	if strings.TrimSpace(fingerprint) == "" {
		return fmt.Errorf("%w: fingerprint is required", models.ErrMissingFingerprint)
	}

	if strings.TrimSpace(uid) == "" {
		return fmt.Errorf("%w: UID is required", models.ErrMissingUID)
	}

	for _, urlStr := range urls {
		if _, err := url.ParseRequestURI(urlStr); err != nil {
			return fmt.Errorf("%w: invalid URL '%s': %w", models.ErrInvalidFormat, urlStr, err)
		}
	}

	return nil
}

// ValidateChunkBatch validates a batch of chunks.
func ValidateChunkBatch(chunks []*models.Chunk) error {
	if len(chunks) == 0 {
		return fmt.Errorf("%w: chunk batch cannot be empty", models.ErrEmptyDocument)
	}

	for i, chunk := range chunks {
		if err := ValidateChunk(chunk); err != nil {
			return fmt.Errorf("chunk %d: %w", i, err)
		}
	}

	return nil
}

// ValidateHierarchicalConsistency ensures chunks maintain proper hierarchical structure.
func ValidateHierarchicalConsistency(chunks []*models.Chunk) error {
	if len(chunks) == 0 {
		return nil
	}

	for i, chunk := range chunks {
		// First chunk should be level 0 or 1
		if i == 0 && chunk.Level > 1 {
			return fmt.Errorf("first chunk has invalid level %d (expected 0 or 1)", chunk.Level)
		}

		// If we have a previous chunk, check the level progression
		if i > 0 {
			prevLevel := chunks[i-1].Level
			currentLevel := chunk.Level

			// Level can increase by at most 1, decrease by any amount, or stay the same
			if currentLevel > prevLevel+1 {
				return fmt.Errorf("chunk %d: level jump from %d to %d is too large", i, prevLevel, currentLevel)
			}
		}

		// Validate that header path makes sense for the level
		if chunk.Level > 0 && chunk.HeaderPath == "" {
			return fmt.Errorf("chunk %d: level %d chunk must have a header path", i, chunk.Level)
		}
	}

	return nil
}

// SanitizeContent removes excessive whitespace and normalizes line endings.
func SanitizeContent(content string) string {
	// Normalize line endings to \n
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")

	// Trim leading/trailing whitespace
	content = strings.TrimSpace(content)

	return content
}

// ExtractHeaderHierarchy extracts the hierarchical path from a chunk's header path.
func ExtractHeaderHierarchy(headerPath string) []string {
	if headerPath == "" {
		return []string{}
	}

	parts := strings.Split(headerPath, " > ")
	return parts
}

// GetChunkLevelFromHeader determines the chunk level based on header prefix.
func GetChunkLevelFromHeader(header string) int {
	// H1 = level 1, H2 = level 2
	if strings.HasPrefix(header, "# ") {
		return 1
	}
	if strings.HasPrefix(header, "## ") {
		return 2
	}
	return 0 // No header or inline content
}

// NormalizeChunkContent normalizes chunk content for consistent processing.
func NormalizeChunkContent(content string) string {
	content = SanitizeContent(content)

	// Remove any markdown header markers from the content itself
	// (they should be in the header path, not the content)
	lines := strings.Split(content, "\n")
	var cleanedLines []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			// Skip lines that are pure headers
			continue
		}
		cleanedLines = append(cleanedLines, line)
	}

	return strings.TrimSpace(strings.Join(cleanedLines, "\n"))
}
