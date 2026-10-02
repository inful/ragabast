package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/service"
)

// TestDarkMode_NoLightModifiersOnAdaptiveSurfaces pins the
// dark-mode contract that came out of the v0.11.4 follow-on:
// Bulma's `is-light` and `has-background-light` modifiers pin
// the element's background to `var(--bulma-light-l)`, which
// stays light (around 96% L) even when
// `prefers-color-scheme: dark` flips the rest of the palette.
// Any element that should adapt to the OS theme must not
// carry those modifiers.
//
// The rendered surfaces that had this bug:
//   - The page navbar (pageHeaderFallbackBody).
//   - The assistant reply box in the chat message fragment
//     (templates/chat_message.html).
//   - Secondary buttons, tags, and notifications that should
//     adapt to the OS scheme.
//
// The test renders each surface and fails if any of the
// "lock to light color" classes leak back in. A future
// contributor tempted to add `is-light` for visual contrast
// in light mode gets a red test, with this comment explaining
// why it breaks dark mode.
func TestDarkMode_NoLightModifiersOnAdaptiveSurfaces(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeService{})
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

	// No `is-light` anywhere on the chat landing page. The
	// navbar, the Sign out button, the Thinking tag, and
	// the empty-state notification all used to carry
	// `is-light`; all of them stay white in dark mode.
	assert.NotContains(t, body, "is-light",
		"the chat landing page must not carry is-light anywhere - Bulma's is-light pins to var(--bulma-light-l) which stays light in dark mode (v0.11.4 follow-on)")

	// No `has-background-light` anywhere. The assistant
	// reply box used to carry this class; in dark mode it
	// rendered as a white panel against the dark page.
	assert.NotContains(t, body, "has-background-light",
		"the chat landing page must not carry has-background-light anywhere - same root cause as is-light")
}

// TestDarkMode_AssistantReplyBoxNoLightBackground pins the
// chat-message fragment specifically. The fragment is
// rendered as the response to POST /chat/message and swapped
// into #chat-messages via htmx; an `is-light` or
// `has-background-light` class on the assistant box surfaces
// as a white panel inside the (otherwise dark) chat log in
// dark mode.
func TestDarkMode_AssistantReplyBoxNoLightBackground(t *testing.T) {
	cfg := config.DefaultConfig()
	svc := &fakeService{
		queryAnswer: "Here is what I found.",
		queryDebug: &service.QueryDebugInfo{
			Results: []models.SearchResult{
				{
					ChunkID:       "c1",
					DocumentID:    "doc-1",
					DocumentTitle: "ADR 001",
					UID:           "adr-001",
					Similarity:    0.91,
				},
			},
		},
	}
	s := NewServer(cfg, svc)

	form := "message=what+does+adr-001+say"
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/chat/message", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	assert.NotContains(t, body, "is-light",
		"the chat-message fragment must not carry is-light anywhere (sign-out button, etc. don't render in this fragment, but be defensive)")
	assert.NotContains(t, body, "has-background-light",
		"the assistant reply box must not carry has-background-light - it stays white in dark mode (v0.11.4 follow-on)")
}

// TestDarkMode_TurnDividerUsesAdaptiveVariable pins the
// chat turn divider to use a Bulma CSS variable rather than
// a hardcoded grey. chat.css used to declare
// `border-top: 1px solid hsl(0 0% 86%)`, which is fine in
// light mode but reads as a glaring light line on the dark
// page in dark mode. `var(--bulma-border-weak)` tracks the
// scheme: 86% in light mode, ~21% in dark mode.
func TestDarkMode_TurnDividerUsesAdaptiveVariable(t *testing.T) {
	css := readStaticAsset(t, "/static/chat.css")
	require.NotEmpty(t, css, "chat.css must be readable from the embedded bundle")

	assert.Regexp(t, `\.chat-turn-divider\s*\{[^}]*border-top:\s*1px solid var\(--bulma-border-weak\)`, css,
		".chat-turn-divider must use var(--bulma-border-weak) so the divider color adapts to prefers-color-scheme")
	assert.NotRegexp(t, `\.chat-turn-divider\s*\{[^}]*hsl\(0\s+0%\s+86%\)`, css,
		".chat-turn-divider must not hardcode a light-mode grey - that color is glaring in dark mode (v0.11.4 follow-on)")
}
