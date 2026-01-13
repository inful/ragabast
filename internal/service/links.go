package service

import (
	"strings"

	"github.com/ragabast/internal/models"
)

func extractURLs(results []models.SearchResult) []string {
	seen := make(map[string]struct{}, 8)
	urls := make([]string, 0, 8)
	for _, result := range results {
		for _, url := range result.DocumentURLs {
			normalized := strings.TrimSpace(url)
			if normalized == "" {
				continue
			}
			if _, ok := seen[normalized]; ok {
				continue
			}
			seen[normalized] = struct{}{}
			urls = append(urls, normalized)
		}
	}
	return urls
}

func hasLinksSection(answer string) bool {
	for line := range strings.SplitSeq(answer, "\n") {
		if strings.TrimSpace(line) == "Links:" {
			return true
		}
	}
	return false
}

func appendLinksSection(answer string, urls []string) string {
	trimmed := strings.TrimRight(answer, "\n")
	if len(urls) == 0 {
		return trimmed
	}
	if hasLinksSection(trimmed) {
		return trimmed
	}

	var b strings.Builder
	b.Grow(len(trimmed) + 16 + (len(urls) * 32))
	if trimmed != "" {
		b.WriteString(trimmed)
		b.WriteString("\n\n")
	}
	b.WriteString("Links:\n")
	for _, url := range urls {
		b.WriteString("- ")
		b.WriteString(url)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
