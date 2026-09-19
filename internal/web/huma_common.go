package web

import (
	"strings"

	"github.com/ragabast/internal/models"
)

// extractLinksFromResults deduplicates and trims the URLs across
// all returned chunks, preserving first-seen order. Used by the
// query and link-suggestions endpoints. Unit-tested in links_test.go.
func extractLinksFromResults(results []models.SearchResult) []string {
	seen := make(map[string]struct{}, 8)
	out := make([]string, 0, 8)
	for _, r := range results {
		for _, u := range r.DocumentURLs {
			url := strings.TrimSpace(u)
			if url == "" {
				continue
			}
			if _, ok := seen[url]; ok {
				continue
			}
			seen[url] = struct{}{}
			out = append(out, url)
		}
	}
	return out
}
