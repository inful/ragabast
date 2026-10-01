package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ragabast/internal/config"
)

// TestChatLanding_NoExportButton pins the contract: the
// chat landing page must not render an "Export transcript"
// affordance. The button was removed in v0.11.2 because
// the feature added maintenance weight (handler, route,
// fakes field, dedicated test file, README docs) without
// matching operator usage — the in-memory session is
// short-lived and operators who wanted to keep a transcript
// could already screenshot or copy/paste.
//
// The test reads the rendered page body so a regression
// that re-adds the link (or any near-clone with a different
// id) gets a red CI rather than a confused operator.
func TestChatLanding_NoExportButton(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeHumaService{})
	s.templates = nil

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
	body := buf.String()

	assert.NotContains(t, body, "Export transcript",
		"the chat landing page must not advertise transcript export — feature removed in v0.11.2")
	assert.NotContains(t, body, `id="chat-export"`,
		"the chat-export anchor must not render — the id is what chat.js or tests would target; its presence means the link came back")
	assert.NotContains(t, body, "/api/chat/export",
		"no anchor must reference the deleted /api/chat/export endpoint")
}

// TestChatExportRouteRemoved pins the HTTP-surface
// contract: GET /api/chat/export must NOT exist on the
// server. With the route unregistered, the standard chi
// router returns 404 for the path.
//
// The test discriminates between "no route registered"
// (chi's default 404 with no body) and "route registered
// but handler 404s" (the handleChatExport body says "No
// chat transcript for this session."). The handler path
// would silently come back if a future contributor
// re-registered the route thinking the feature still
// existed; the body check catches that.
func TestChatExportRouteRemoved(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeHumaService{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/chat/export?session_id=any", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code,
		"/api/chat/export must 404 after the feature removal — if this returns 200 the route came back")
	assert.NotContains(t, w.Body.String(), "No chat transcript",
		"a 404 from the deleted-route path has no body; a 404 with the transcript-error message means the route was re-registered")
}
