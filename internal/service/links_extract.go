package service

import (
	"regexp"
	"strings"

	"github.com/ragabast/internal/models"
)

var (
	answerURLRegex                   = regexp.MustCompile(`https?://[^\s<>"'` + "`" + `]+`)
	markdownLinkRegex                = regexp.MustCompile(`\[([^\]]*)\]\((https?://[^\s)<>"'` + "`" + `]+)\)`)
	autoLinkRegex                    = regexp.MustCompile(`<\s*(https?://[^\s<>"'` + "`" + `]+)\s*>`)
	emptyMarkdownLinkRegex           = regexp.MustCompile(`\[\s*\]\(\s*\)`)
	emptyMarkdownLinkWithTextRegex   = regexp.MustCompile(`\[([^\]]+)\]\(\s*\)`)
	spacesBeforePunctRegex           = regexp.MustCompile(`[ \t]+([).,;:!?}\]])`)
	spacesBeforeClosingBacktickRegex = regexp.MustCompile(`[ \t]+(` + "`" + `)([ \t).,;:!?}\]])`)
	spacesBeforeFinalBacktickRegex   = regexp.MustCompile(`[ \t]+` + "`" + `$`)
	multiSpaceRegex                  = regexp.MustCompile(`[ \t]{2,}`)
)

func extractURLs(results []models.SearchResult) []string {
	seen := make(map[string]struct{}, 8)
	urls := make([]string, 0, 8)
	add := func(url string) {
		normalized := strings.TrimSpace(url)
		if normalized == "" {
			return
		}
		if _, ok := seen[normalized]; ok {
			return
		}
		seen[normalized] = struct{}{}
		urls = append(urls, normalized)
	}
	for _, result := range results {
		for _, url := range result.DocumentURLs {
			add(url)
		}
		for _, url := range extractURLsFromText(result.Content) {
			add(url)
		}
	}
	return urls
}

func extractURLsFromText(text string) []string {
	if strings.TrimSpace(text) == "" {
		return nil
	}

	matches := answerURLRegex.FindAllString(text, -1)
	if len(matches) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(matches))
	urls := make([]string, 0, len(matches))
	for _, match := range matches {
		base, _ := splitURLTrailingPunct(match)
		base = strings.TrimSpace(base)
		if base == "" {
			continue
		}
		if _, ok := seen[base]; ok {
			continue
		}
		seen[base] = struct{}{}
		urls = append(urls, base)
	}
	return urls
}
