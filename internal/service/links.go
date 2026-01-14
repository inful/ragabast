package service

import (
	"regexp"
	"strings"

	"github.com/ragabast/internal/models"
)

var (
	answerURLRegex                   = regexp.MustCompile("https?://[^\\s<>\"'`]+")
	markdownLinkRegex                = regexp.MustCompile("\\[([^\\]]*)\\]\\((https?://[^\\s)<>\\\"'`]+)\\)")
	autoLinkRegex                    = regexp.MustCompile("<\\s*(https?://[^\\s<>\\\"'`]+)\\s*>")
	emptyMarkdownLinkRegex           = regexp.MustCompile(`\[\s*\]\(\s*\)`)
	emptyMarkdownLinkWithTextRegex   = regexp.MustCompile(`\[([^\]]+)\]\(\s*\)`)
	spacesBeforePunctRegex           = regexp.MustCompile(`[ \t]+([).,;:!?}\]])`)
	spacesBeforeClosingBacktickRegex = regexp.MustCompile("[ \\t]+(`)([ \\t).,;:!?}\\]])")
	spacesBeforeFinalBacktickRegex   = regexp.MustCompile("[ \\t]+`$")
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

func appendLinksSection(answer string, urls []string) string {
	return enforceLinksPolicy(answer, urls)
}

func enforceLinksPolicy(answer string, allowedURLs []string) string {
	allowed := make(map[string]struct{}, len(allowedURLs))
	for _, u := range allowedURLs {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		allowed[u] = struct{}{}
	}

	trimmed := strings.TrimRight(answer, "\n")

	// Remove any existing Links section (the model sometimes hallucinates URLs).
	trimmed = stripLinksSection(trimmed)

	// Remove any non-allowed URLs from the body.
	trimmed = scrubNonAllowedURLs(trimmed, allowed)

	// Append canonical Links section when we have sources.
	if len(allowed) == 0 {
		return strings.TrimRight(trimmed, "\n")
	}

	var b strings.Builder
	b.Grow(len(trimmed) + 16 + (len(allowedURLs) * 32))
	if strings.TrimSpace(trimmed) != "" {
		b.WriteString(strings.TrimRight(trimmed, "\n"))
		b.WriteString("\n\n")
	}
	b.WriteString("Links:\n")
	for _, url := range allowedURLs {
		url = strings.TrimSpace(url)
		if url == "" {
			continue
		}
		if _, ok := allowed[url]; !ok {
			continue
		}
		b.WriteString("- ")
		b.WriteString(url)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func stripLinksSection(answer string) string {
	lines := strings.Split(answer, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)
		if lower == "links:" || strings.HasPrefix(lower, "links:") {
			out := strings.Join(lines[:i], "\n")
			return strings.TrimRight(out, "\n")
		}
	}
	return answer
}

func scrubNonAllowedURLs(answer string, allowed map[string]struct{}) string {
	if answer == "" {
		return ""
	}

	// First, handle markdown links so we don't leave broken artifacts when removing URLs.
	// - Disallowed: replace with the link text (or empty if link text is just a URL).
	// - Allowed: keep as-is (but normalize trailing punctuation inside the URL).
	replaced := markdownLinkRegex.ReplaceAllStringFunc(answer, func(match string) string {
		parts := markdownLinkRegex.FindStringSubmatch(match)
		if len(parts) != 3 {
			return match
		}
		text := parts[1]
		url := parts[2]
		base, suffix := splitURLTrailingPunct(url)
		if len(allowed) == 0 {
			trimmedText := strings.TrimSpace(answerURLRegex.ReplaceAllString(text, ""))
			if trimmedText == "" {
				return ""
			}
			return trimmedText
		}
		if _, ok := allowed[base]; ok {
			return "[" + text + "](" + base + suffix + ")"
		}
		trimmedText := strings.TrimSpace(answerURLRegex.ReplaceAllString(text, ""))
		if trimmedText == "" {
			return ""
		}
		return trimmedText
	})

	// Handle autolinks like <https://example.com>.
	replaced = autoLinkRegex.ReplaceAllStringFunc(replaced, func(match string) string {
		parts := autoLinkRegex.FindStringSubmatch(match)
		if len(parts) != 2 {
			return match
		}
		url := parts[1]
		base, suffix := splitURLTrailingPunct(url)
		if len(allowed) == 0 {
			return ""
		}
		if _, ok := allowed[base]; ok {
			return "<" + base + suffix + ">"
		}
		return ""
	})

	// Then strip any remaining bare URLs.
	replaced = answerURLRegex.ReplaceAllStringFunc(replaced, func(match string) string {
		base, suffix := splitURLTrailingPunct(match)
		if len(allowed) == 0 {
			// If there are no known sources, strip all URLs.
			return suffix
		}
		if _, ok := allowed[base]; ok {
			return base + suffix
		}
		return suffix
	})

	// Clean up markdown artifacts introduced by URL stripping.
	replaced = emptyMarkdownLinkRegex.ReplaceAllString(replaced, "")
	replaced = emptyMarkdownLinkWithTextRegex.ReplaceAllString(replaced, "$1")

	// Clean up whitespace introduced by URL stripping.
	replaced = spacesBeforePunctRegex.ReplaceAllString(replaced, "$1")
	replaced = spacesBeforeClosingBacktickRegex.ReplaceAllString(replaced, "$1$2")
	replaced = spacesBeforeFinalBacktickRegex.ReplaceAllString(replaced, "`")
	replaced = multiSpaceRegex.ReplaceAllString(replaced, " ")
	return strings.TrimSpace(replaced)
}

func splitURLTrailingPunct(url string) (base string, suffix string) {
	if url == "" {
		return "", ""
	}
	const trailing = ").,;:!?]}"
	base = strings.TrimRight(url, trailing)
	suffix = url[len(base):]
	return base, suffix
}
