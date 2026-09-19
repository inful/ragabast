package vector

import (
	"strconv"
	"strings"
	"time"

	"github.com/ragabast/internal/models"
)

// chunkToMetadata builds the metadata map stored alongside a
// chunk in the chromem-go collection. Optional fields (URLs,
// tags, categories, timestamps, fingerprint, UID, parent ID) are
// omitted when empty so the metadata stays lean.
//
// Used by both AddChunk and AddChunksBatch; the round-trip
// inverse is metadataToChunk below.
func chunkToMetadata(chunkID string, chunk *models.Chunk) map[string]string {
	meta := map[string]string{
		"document_id":    chunk.DocumentID,
		"chunk_id":       chunkID,
		"header_path":    chunk.HeaderPath,
		"level":          strconv.Itoa(chunk.Level),
		"start_line":     strconv.Itoa(chunk.StartLine),
		"end_line":       strconv.Itoa(chunk.EndLine),
		"document_title": chunk.DocumentTitle,
	}
	if len(chunk.DocumentURLs) > 0 {
		meta["document_urls"] = strings.Join(chunk.DocumentURLs, "\n")
	}
	if len(chunk.DocumentTags) > 0 {
		meta["document_tags"] = strings.Join(chunk.DocumentTags, "\n")
	}
	if len(chunk.DocumentCategories) > 0 {
		meta["document_categories"] = strings.Join(chunk.DocumentCategories, "\n")
	}
	if !chunk.DocumentCreatedAt.IsZero() {
		meta["document_created_at"] = chunk.DocumentCreatedAt.UTC().Format(time.RFC3339)
	}
	if !chunk.DocumentUpdatedAt.IsZero() {
		meta["document_updated_at"] = chunk.DocumentUpdatedAt.UTC().Format(time.RFC3339)
	}
	if chunk.Fingerprint != "" {
		meta["fingerprint"] = chunk.Fingerprint
	}
	if chunk.UID != "" {
		meta["uid"] = chunk.UID
	}
	if chunk.ParentID != "" {
		meta["parent_id"] = chunk.ParentID
	}
	return meta
}

// metadataToChunk reverses chunkToMetadata: pulls back the
// numeric, list, and timestamp fields stored in metadata and
// returns a fully populated *models.Chunk. Used by GetChunk and
// GetChunksByDocument. The chunk ID comes from the "chunk_id"
// metadata key written by chunkToMetadata.
func metadataToChunk(meta map[string]string, content string) *models.Chunk {
	level := 0
	startLine := 0
	endLine := 0
	if v, ok := meta["level"]; ok {
		level, _ = strconv.Atoi(v)
	}
	if v, ok := meta["start_line"]; ok {
		startLine, _ = strconv.Atoi(v)
	}
	if v, ok := meta["end_line"]; ok {
		endLine, _ = strconv.Atoi(v)
	}

	return &models.Chunk{
		ID:                 meta["chunk_id"],
		DocumentID:         meta["document_id"],
		Content:            content,
		HeaderPath:         meta["header_path"],
		Level:              level,
		StartLine:          startLine,
		EndLine:            endLine,
		DocumentTitle:      meta["document_title"],
		Fingerprint:        meta["fingerprint"],
		UID:                meta["uid"],
		ParentID:           meta["parent_id"],
		DocumentURLs:       splitNonEmptyLines(meta["document_urls"]),
		DocumentTags:       splitNonEmptyLines(meta["document_tags"]),
		DocumentCategories: splitNonEmptyLines(meta["document_categories"]),
		DocumentCreatedAt:  parseRFC3339(meta["document_created_at"]),
		DocumentUpdatedAt:  parseRFC3339(meta["document_updated_at"]),
	}
}

// splitMetadataList splits a metadata value that was stored as
// newline-joined items into a slice with empty entries filtered out.
// Returns nil for empty input so the SearchResult keeps the
// `omitempty` JSON contract.
func splitMetadataList(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, "\n")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// splitNonEmptyLines is the same shape as splitMetadataList
// but returns an empty slice (not nil) for empty input, which is
// what the metadata→chunk conversion expects (a nil slice there
// would JSON-encode as null inside the chunk).
func splitNonEmptyLines(s string) []string {
	if s == "" {
		return []string{}
	}
	parts := strings.Split(s, "\n")
	filtered := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		filtered = append(filtered, p)
	}
	return filtered
}

func parseRFC3339(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return parsed
}
