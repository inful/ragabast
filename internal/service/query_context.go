package service

import (
	"context"
	"log"
	"strings"

	"github.com/ragabast/internal/models"
)

// ChunkFetcher returns a parent chunk's body given its ID, or
// (nil, false) if the chunk is not available. It is the seam
// between the prompt builder and the vector store: tests pass a
// stub; production passes vectorChunkFetcher below.
type ChunkFetcher interface {
	FetchChunk(ctx context.Context, id string) (*models.Chunk, bool, error)
}

// vectorChunkFetcher adapts the Service's GetChunk method to the
// ChunkFetcher interface above, so the prompt builder can pull
// parent context without depending on the vector package directly.
type vectorChunkFetcher struct {
	s *Service
}

func (v vectorChunkFetcher) FetchChunk(ctx context.Context, id string) (*models.Chunk, bool, error) {
	chunk, err := v.s.GetChunk(ctx, id)
	if err != nil {
		return nil, false, err
	}
	if chunk == nil {
		return nil, false, nil
	}
	return chunk, true, nil
}

// buildQueryContextItems formats the retrieved chunks for the LLM.
//
// Each item is a small block containing:
//   - the parent document's title
//   - the chunk's section path (HeaderPath)
//   - the document's tags and categories, when present
//   - the parent chunk's body (when this is a child chunk and the
//     parent is not already represented in the result set), so the
//     LLM has the section header and the lead-in context
//   - the chunk's own body
//   - the SOURCE_URLS line, when the document has any
//
// The model cites entries by index.
func buildQueryContextItems(ctx context.Context, results []models.SearchResult, fetcher ChunkFetcher) []string {
	presentChunkIDs := make(map[string]struct{}, len(results))
	for _, r := range results {
		if r.ChunkID != "" {
			presentChunkIDs[r.ChunkID] = struct{}{}
		}
	}

	items := make([]string, 0, len(results))
	for _, result := range results {
		items = append(items, formatContextEntry(ctx, result, presentChunkIDs, fetcher))
	}
	return items
}

// resolveParentContext returns the trimmed body of the parent chunk
// when one should be prepended to the result, or "" if no parent
// applies (root chunk, parent already in result set, missing chunk,
// fetch error, or empty body). Errors are logged but do not block
// prompt building.
func resolveParentContext(ctx context.Context, result models.SearchResult, presentChunkIDs map[string]struct{}, fetcher ChunkFetcher) string {
	if result.ParentID == "" {
		return ""
	}
	if _, already := presentChunkIDs[result.ParentID]; already {
		return ""
	}
	if fetcher == nil {
		return ""
	}
	parent, ok, err := fetcher.FetchChunk(ctx, result.ParentID)
	if err != nil {
		log.Printf("prompt: fetch parent %q for chunk %q: %v", result.ParentID, result.ChunkID, err)
		return ""
	}
	if !ok || parent == nil {
		return ""
	}
	return strings.TrimSpace(parent.Content)
}

func formatContextEntry(ctx context.Context, result models.SearchResult, presentChunkIDs map[string]struct{}, fetcher ChunkFetcher) string {
	var b strings.Builder
	b.WriteString("TITLE: ")
	b.WriteString(strings.TrimSpace(result.DocumentTitle))
	b.WriteByte('\n')

	if section := strings.TrimSpace(result.HeaderPath); section != "" {
		b.WriteString("SECTION: ")
		b.WriteString(section)
		b.WriteByte('\n')
	}

	if len(result.DocumentTags) > 0 {
		b.WriteString("TAGS: ")
		b.WriteString(strings.Join(result.DocumentTags, ", "))
		b.WriteByte('\n')
	}
	if len(result.DocumentCategories) > 0 {
		b.WriteString("CATEGORIES: ")
		b.WriteString(strings.Join(result.DocumentCategories, ", "))
		b.WriteByte('\n')
	}

	if parentBody := resolveParentContext(ctx, result, presentChunkIDs, fetcher); parentBody != "" {
		b.WriteString("PARENT_CONTEXT:\n")
		b.WriteString(parentBody)
		b.WriteByte('\n')
	}

	b.WriteString("CONTENT:\n")
	b.WriteString(result.Content)

	if urls := collectContextURLs(result); len(urls) > 0 {
		b.WriteString("\nSOURCE_URLS: ")
		b.WriteString(strings.Join(urls, ", "))
	}
	return b.String()
}

// collectContextURLs returns the URLs for a single result, deduped,
// with the document's declared URLs first and any URLs found inside
// the chunk body appended.
func collectContextURLs(result models.SearchResult) []string {
	seen := make(map[string]struct{}, len(result.DocumentURLs)+2)
	urls := make([]string, 0, len(result.DocumentURLs)+2)
	for _, url := range result.DocumentURLs {
		url = strings.TrimSpace(url)
		if url == "" {
			continue
		}
		if _, ok := seen[url]; ok {
			continue
		}
		seen[url] = struct{}{}
		urls = append(urls, url)
	}
	for _, url := range extractURLsFromText(result.Content) {
		if _, ok := seen[url]; ok {
			continue
		}
		seen[url] = struct{}{}
		urls = append(urls, url)
	}
	return urls
}
