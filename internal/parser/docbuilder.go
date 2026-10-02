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

// DocbuilderParser parses markdown documents with YAML frontmatter in the docbuilder format.
type DocbuilderParser struct{}

// NewDocbuilderParser creates a new parser instance.
func NewDocbuilderParser() *DocbuilderParser {
	return &DocbuilderParser{}
}

// ParseDocument parses a document from raw bytes.
func (p *DocbuilderParser) ParseDocument(rawContent []byte, filePath string) (*models.Document, error) {
	doc := models.NewDocument()
	doc.RawContent = rawContent
	doc.FilePath = filePath

	// Normalize CRLF to LF for parsing. Windows editors, chat
	// clients, and browser textareas (which preserve the user's
	// paste including CR) all hand us text with \r\n; the
	// frontmatter delimiters and the chunker's line-splitting
	// both expect LF. Without this, CRLF pastes either fail
	// frontmatter extraction entirely (silent empty-UID bug)
	// or produce chunks with a trailing \r on every line.
	// RawContent keeps the original bytes so logs and re-ingest
	// paths see exactly what the operator uploaded.
	normalized := bytes.ReplaceAll(rawContent, []byte("\r\n"), []byte("\n"))

	// Extract frontmatter and the markdown body that follows it.
	frontmatter, body, err := p.extractFrontmatter(normalized)
	if err != nil {
		return nil, fmt.Errorf("failed to extract frontmatter: %w", err)
	}

	// Parse frontmatter
	if err := p.parseFrontmatter(frontmatter, doc); err != nil {
		return nil, fmt.Errorf("failed to parse frontmatter: %w", err)
	}

	// Set content from the body (frontmatter already stripped).
	doc.Content = strings.TrimSpace(string(body))

	// Compute fingerprint when not explicitly provided.
	// Docbuilder convention: "fingerprint: auto-generated-if-empty" means the system should compute it.
	if strings.TrimSpace(doc.Fingerprint) == "" {
		doc.Fingerprint = p.generateFingerprint(doc.Content)
	}

	// Make document IDs stable (dedupe-friendly).
	// Use the docbuilder UID as the stable identifier; fingerprint is strictly content-based.
	doc.ID = doc.UID

	// Validate required fields
	if err := doc.Validate(); err != nil {
		return nil, fmt.Errorf("document validation failed: %w", err)
	}

	return doc, nil
}

// extractFrontmatter separates YAML frontmatter from markdown content.
func (p *DocbuilderParser) extractFrontmatter(raw []byte) ([]byte, []byte, error) {
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
func (p *DocbuilderParser) parseFrontmatter(data []byte, doc *models.Document) error {
	if len(data) == 0 {
		return nil
	}

	var frontmatter map[string]any
	if err := yaml.Unmarshal(data, &frontmatter); err != nil {
		return fmt.Errorf("invalid YAML: %w", err)
	}

	// Extract SourceKind (typed field, gitlab writer sets "source_kind: gitlab").
	// Operators writing docbuilder markdown shouldn't set this unless they
	// intentionally want the per-source citation dispatch + post-filter to
	// treat their doc as a non-default kind.
	if k, ok := frontmatter["source_kind"].(string); ok {
		doc.SourceKind = models.SourceKind(k)
	}

	// hugoFields is the lowercased lookup map for the four Hugo
	// "don't publish" markers and their aliases. Hugo itself is
	// case-insensitive on field names; yaml.v3 is case-sensitive
	// on map keys when unmarshaling into map[string]any. Without
	// this normalization step, `publishDate:` and `publishdate:`
	// would each parse but only one would be reachable by a
	// case-sensitive lookup.
	hugoFields := lowerHugoKeys(frontmatter)
	if v, ok := hugoFields["draft"]; ok {
		if b, ok := v.(bool); ok {
			doc.Draft = b
		}
	}
	if v, ok := hugoFields["date"]; ok {
		if t, ok := parseHugoTime(v); ok {
			doc.Date = t
		}
	}
	// publishDate wins over pubdate over published — first non-empty
	// wins, matching Hugo's own lookup order.
	if t := firstHugoTime(hugoFields, "publishdate", "pubdate", "published"); !t.IsZero() {
		doc.PublishDate = t
	}
	if t := firstHugoTime(hugoFields, "expirydate", "unpublishdate"); !t.IsZero() {
		doc.ExpiryDate = t
	}

	// Extract fingerprint
	if fp, ok := frontmatter["fingerprint"].(string); ok {
		// Docbuilder convention: this placeholder means "compute it".
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

	// Extract title. Strict precedence: the frontmatter `title:`
	// field is THE title. There is no H1 fallback — a document
	// without a frontmatter `title:` has an empty doc.Title,
	// which the presentation layer (DisplayLabel) renders via
	// its filename → document_id fallback chain.
	if title, ok := frontmatter["title"].(string); ok {
		doc.Title = strings.TrimSpace(title)
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

	// Copy non-typed frontmatter keys into doc.Metadata so the
	// gitlab writer's "state" / "author_username" (and any future
	// source-specific keys) flow through to chunk.Metadata during
	// the ingest pipeline. The typedKeys set is the explicit
	// allow-list of fields the parser recognizes — anything not in
	// it lands in Metadata. Keep this list in sync with the typed
	// extractions above AND with the Hugo fields handled at the
	// top of this function.
	doc.Metadata = extractUntypedFrontmatter(frontmatter, typedFrontmatterKeys)

	return nil
}

// typedFrontmatterKeys is the closed allow-list of frontmatter keys
// the parser recognizes as typed fields. Any other top-level key in
// the YAML frontmatter flows into doc.Metadata via
// extractUntypedFrontmatter. Hugo's "don't publish" markers and
// their case-insensitive aliases are also excluded (handled
// separately via lowerHugoKeys).
//
// Add a key here when you give it a typed extraction in
// parseFrontmatter; do NOT add a key without a corresponding
// extraction, or that field will be silently discarded from
// chunk.Metadata.
var typedFrontmatterKeys = map[string]struct{}{
	"fingerprint": {},
	"uid":         {},
	"tags":        {},
	"categories":  {},
	"urls":        {},
	"title":       {},
	"created_at":  {},
	"updated_at":  {},
	"source_kind": {},
	// hugoFields (draft, date, publishdate, expirydate, etc.) are
	// matched case-insensitively and would also be excluded, but
	// we don't need them in the metadata bag anyway.
}

// extractUntypedFrontmatter returns a map[string]string of every
// top-level frontmatter key that is not in typedKeys and that has
// a string scalar value. Non-string values (lists, maps, nested
// structures) are skipped — they don't fit the chunk.Metadata
// contract (a flat string→string map) and trying to coerce them
// would surprise the writer. Returns nil for empty input so the
// doc.Metadata field stays lean with omitempty.
//
// Why this is generic: a source writer (gitlab, future redmine,
// ...) can introduce a new metadata key without coordinating with
// the parser. The parser just plumbs it through; the writer and
// the downstream filter agree on the key names. See the
// MetadataKey* constants in internal/gitlab/payload.go for the
// gitlab-side wire contract.
func extractUntypedFrontmatter(fm map[string]any, typed map[string]struct{}) map[string]string {
	var out map[string]string
	for k, v := range fm {
		if _, isTyped := typed[k]; isTyped {
			continue
		}
		s, ok := v.(string)
		if !ok {
			continue
		}
		if out == nil {
			out = make(map[string]string)
		}
		out[k] = s
	}
	return out
}

// generateFingerprint creates a stable content fingerprint.
func (p *DocbuilderParser) generateFingerprint(content string) string {
	return mdfp.CalculateFingerprint(content)
}

// ExtractHeaders extracts all H1 and H2 headers with their positions.
func (p *DocbuilderParser) ExtractHeaders(content string) []models.HeaderInfo {
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
