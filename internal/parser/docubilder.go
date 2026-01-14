package parser

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/inful/mdfp"
	"github.com/ragabast/internal/models"
	"gopkg.in/yaml.v3"
)

// DocubilderParser parses markdown documents with YAML frontmatter in the docubilder format.
type DocubilderParser struct{}

// NewDocubilderParser creates a new parser instance.
func NewDocubilderParser() *DocubilderParser {
	return &DocubilderParser{}
}

// ParseDocument parses a document from raw bytes.
func (p *DocubilderParser) ParseDocument(rawContent []byte, filePath string) (*models.Document, error) {
	doc := models.NewDocument()
	doc.RawContent = rawContent
	doc.FilePath = filePath

	// Extract frontmatter and content
	frontmatter, content, err := p.extractFrontmatter(rawContent)
	if err != nil {
		return nil, fmt.Errorf("failed to extract frontmatter: %w", err)
	}

	// Parse frontmatter
	if err := p.parseFrontmatter(frontmatter, doc); err != nil {
		return nil, fmt.Errorf("failed to parse frontmatter: %w", err)
	}

	// Set content
	doc.Content = strings.TrimSpace(string(content))

	// Make document IDs stable (dedupe-friendly).
	// Use the docubilder UID as the stable identifier; fingerprint is strictly content-based.
	doc.ID = doc.UID

	// Extract title from first H1 header
	doc.Title = p.extractTitle(doc.Content)

	// Validate required fields
	if err := doc.Validate(); err != nil {
		return nil, fmt.Errorf("document validation failed: %w", err)
	}

	return doc, nil
}

// extractFrontmatter separates YAML frontmatter from markdown content.
func (p *DocubilderParser) extractFrontmatter(raw []byte) ([]byte, []byte, error) {
	content := bytes.TrimSpace(raw)

	// Check for frontmatter delimiter
	if !bytes.HasPrefix(content, []byte("---\n")) {
		// No frontmatter, treat entire content as markdown
		return nil, content, nil
	}

	// Find the end of frontmatter
	endIdx := bytes.Index(content[4:], []byte("\n---\n"))
	if endIdx == -1 {
		return nil, nil, errors.New("frontmatter delimiter not found")
	}

	endIdx += 4 // Adjust for the initial "---\n"
	frontmatter := bytes.TrimSpace(content[4:endIdx])
	markdown := bytes.TrimSpace(content[endIdx+6:])

	return frontmatter, markdown, nil
}

// parseFrontmatter parses YAML frontmatter into the document structure.
func (p *DocubilderParser) parseFrontmatter(data []byte, doc *models.Document) error {
	if len(data) == 0 {
		return nil
	}

	var frontmatter map[string]any
	if err := yaml.Unmarshal(data, &frontmatter); err != nil {
		return fmt.Errorf("invalid YAML: %w", err)
	}

	// Extract fingerprint
	if fp, ok := frontmatter["fingerprint"].(string); ok {
		// Docubilder convention: this placeholder means "compute it".
		if fp != "auto-generated-if-empty" {
			doc.Fingerprint = fp
		}
	}

	// Extract UID
	if uid, ok := frontmatter["uid"].(string); ok {
		doc.UID = uid
	}

	// Extract tags
	if tags, ok := frontmatter["tags"].([]any); ok {
		for _, tag := range tags {
			if tagStr, ok := tag.(string); ok {
				doc.AddTag(tagStr)
			}
		}
	}

	// Extract categories
	if categories, ok := frontmatter["categories"].([]any); ok {
		for _, cat := range categories {
			if catStr, ok := cat.(string); ok {
				doc.AddCategory(catStr)
			}
		}
	}

	// Extract URLs
	if urls, ok := frontmatter["urls"].([]any); ok {
		for _, url := range urls {
			if urlStr, ok := url.(string); ok {
				doc.AddURL(urlStr)
			}
		}
	}

	// Extract created_at
	if created, ok := frontmatter["created_at"].(string); ok {
		if t, err := time.Parse(time.RFC3339, created); err == nil {
			doc.CreatedAt = t
		}
	}

	// Extract updated_at
	if updated, ok := frontmatter["updated_at"].(string); ok {
		if t, err := time.Parse(time.RFC3339, updated); err == nil {
			doc.UpdatedAt = t
		}
	}

	return nil
}

// generateFingerprint creates a stable content fingerprint.
func (p *DocubilderParser) generateFingerprint(content string) string {
	return mdfp.CalculateFingerprint(content)
}

// extractTitle finds the first H1 header in the markdown content.
func (p *DocubilderParser) extractTitle(content string) string {
	lines := strings.SplitSeq(content, "\n")
	for line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			return strings.TrimSpace(trimmed[2:])
		}
	}
	return ""
}

// ParseChunk parses a chunk of content (used for chunking operations).
func (p *DocubilderParser) ParseChunk(content string, headerPath string, level int, startLine, endLine int) *models.Chunk {
	chunk := models.NewChunk()
	chunk.Content = content
	chunk.HeaderPath = headerPath
	chunk.Level = level
	chunk.StartLine = startLine
	chunk.EndLine = endLine
	return chunk
}

// ValidateFrontmatter checks if the frontmatter contains all required fields.
func (p *DocubilderParser) ValidateFrontmatter(raw []byte) error {
	frontmatterBytes, _, err := p.extractFrontmatter(raw)
	if err != nil {
		return err
	}
	if len(frontmatterBytes) == 0 {
		return models.ErrInvalidFrontmatter
	}

	var fm map[string]any
	if err := yaml.Unmarshal(frontmatterBytes, &fm); err != nil {
		return err
	}

	// Check for required fields
	required := []string{"uid"}
	for _, field := range required {
		if _, ok := fm[field]; !ok {
			return fmt.Errorf("missing required field: %s", field)
		}
	}

	// Validate URLs is an array when present
	if rawURLs, ok := fm["urls"]; ok {
		if _, ok := rawURLs.([]any); !ok {
			return errors.New("urls must be an array")
		}
	}

	return nil
}

// FormatFrontmatter formats a document's metadata back into YAML frontmatter.
func (p *DocubilderParser) FormatFrontmatter(doc *models.Document) ([]byte, error) {
	frontmatter := map[string]any{
		"fingerprint": doc.Fingerprint,
		"uid":         doc.UID,
		"tags":        doc.Tags,
		"categories":  doc.Categories,
		"urls":        doc.URLs,
		"created_at":  doc.CreatedAt.Format(time.RFC3339),
		"updated_at":  doc.UpdatedAt.Format(time.RFC3339),
	}

	data, err := yaml.Marshal(frontmatter)
	if err != nil {
		return nil, err
	}

	return append([]byte("---\n"), append(data, []byte("---\n")...)...), nil
}

// ExtractHeaders extracts all H1 and H2 headers with their positions.
func (p *DocubilderParser) ExtractHeaders(content string) []models.HeaderInfo {
	var headers []models.HeaderInfo
	lines := strings.Split(content, "\n")

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			headers = append(headers, models.HeaderInfo{
				Level: 1,
				Text:  strings.TrimSpace(trimmed[2:]),
				Line:  i,
			})
		} else if strings.HasPrefix(trimmed, "## ") {
			headers = append(headers, models.HeaderInfo{
				Level: 2,
				Text:  strings.TrimSpace(trimmed[3:]),
				Line:  i,
			})
		}
	}

	return headers
}

// GenerateFingerprint creates a SHA256 hash of the content (public version).
func (p *DocubilderParser) GenerateFingerprint(content string) string {
	return p.generateFingerprint(content)
}
