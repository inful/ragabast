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

// sanitizeForChatHTML runs a final bluemonday UGCPolicy pass
// over the markdown-rendered HTML before it is wrapped as
// template.HTML in the chat page (see handleChatMessage in
// handlers.go).
//
// Why this exists: the chat page wraps {{ .AnswerHTML }} as
// template.HTML so html/template does not double-escape the
// markdown tags we legitimately rendered. Every other field on
// the page goes through html/template's auto-escape, so this
// one field becomes "trusted by convention" — a security
// smell. To keep that trust explicit, we re-sanitize right
// before the wrap. If the markdown pipeline, InlineSourceLinks,
// or bluemonday itself ever introduces an XSS-class bypass, the
// final pass catches it.
//
// The pass is intentionally identical to chatSanitizer so
// callers do not need to remember which UGCPolicy variant is
// the "outermost" one.
func sanitizeForChatHTML(html string) string {
	return string(chatSanitizer.SanitizeBytes([]byte(html)))
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
