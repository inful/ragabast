package web

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	renderer "github.com/yuin/goldmark/renderer/html"
)

var (
	chatMarkdown = goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			extension.Linkify,
		),
		goldmark.WithRendererOptions(
			renderer.WithHardWraps(),
		),
	)

	chatSanitizer = func() *bluemonday.Policy {
		p := bluemonday.UGCPolicy()
		p.AllowAttrs("target").OnElements("a")
		p.AllowAttrs("rel").OnElements("a")
		return p
	}()

	anchorTagRegex = regexp.MustCompile(`(?i)<a\b[^>]*>`)
)

func renderChatMarkdownToSafeHTML(md string) (string, error) {
	var buf bytes.Buffer
	if err := chatMarkdown.Convert([]byte(md), &buf); err != nil {
		return "", err
	}

	// Sanitize to remove dangerous URLs (e.g. javascript:) and unsafe attrs.
	safe := chatSanitizer.SanitizeBytes(buf.Bytes())

	// Ensure all links open in a new tab.
	withTargets := addTargetBlankToAnchors(string(safe))
	return withTargets, nil
}

func addTargetBlankToAnchors(html string) string {
	return anchorTagRegex.ReplaceAllStringFunc(html, func(tag string) string {
		lower := strings.ToLower(tag)
		if strings.Contains(lower, " target=") {
			return tag
		}
		// Insert attributes right after "<a".
		idx := strings.Index(lower, "<a")
		if idx == -1 {
			return tag
		}
		insertAt := idx + len("<a")
		return tag[:insertAt] + ` target="_blank" rel="noopener noreferrer"` + tag[insertAt:]
	})
}
