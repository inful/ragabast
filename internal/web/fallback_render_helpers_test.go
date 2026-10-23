package web

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// renderChatFallbackBody executes the chat landing fallback
// template and returns the rendered HTML.
//
// One helper for all three UX suites (the navbar, the toast
// container and the polish sweep) — they all render the same
// fallback template with the same data.
func renderChatFallbackBody(t *testing.T, s *Server) string {
	t.Helper()
	data := chatFallbackData{
		Title:     "Chat",
		CsrfToken: "",
		SessionID: "session-test",
		Header: pageHeaderData{
			AuthEnabled: false, SignedIn: false, ShowSignIn: false,
		},
	}
	var buf strings.Builder
	require.NoError(t, s.fallback.chat.Execute(&buf, data))
	return buf.String()
}

// renderDocumentsFallbackBody executes the documents page fallback
// template. Empty documents list — the page renders the empty
// state, which still needs the toast container for delete
// confirmations.
func renderDocumentsFallbackBody(t *testing.T, s *Server) string {
	t.Helper()
	data := documentsFallbackData{
		Title:     "Documents",
		CsrfToken: "",
		Header: pageHeaderData{
			AuthEnabled: false, SignedIn: false, ShowSignIn: false,
		},
	}
	var buf strings.Builder
	require.NoError(t, s.fallback.documents.Execute(&buf, data))
	return buf.String()
}
