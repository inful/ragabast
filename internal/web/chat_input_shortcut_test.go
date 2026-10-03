package web

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ragabast/internal/config"
)

// TestChatAssets_KeyboardSubmitHandler pins the chat-input
// keyboard shortcut that lets the operator submit a message
// without taking their hands off the keyboard. The contract:
//
//   - Enter on its own inserts a newline (textarea browser
//     default — the keydown handler must NOT preventDefault on
//     plain Enter).
//   - Ctrl+Enter (Windows / Linux) submits the form.
//   - Cmd+Enter (macOS) submits the form — same gesture, the
//     platform's modifier key.
//
// The agent binding itself remains true: the form still submits
// on the Send button. The shortcut is additive, not a
// replacement — keyboard-driven operators get parity with
// mouse-driven ones without losing multi-line input.
func TestChatAssets_KeyboardSubmitHandler(t *testing.T) {
	body := readStaticAsset(t, "/static/chat.js")

	// The handler must listen on keydown so it can intercept
	// before the browser default (newline in the textarea, or
	// a stray submit on some setups) takes over.
	require.Contains(t, body, "keydown",
		"chat.js must install a keydown listener on the chat input so the shortcut intercepts before the browser default")

	// Both modifiers are honored. The handler must check
	// ctrlKey OR metaKey so the same gesture works on every
	// platform without a UA-sniff branch.
	require.Contains(t, body, "ctrlKey",
		"chat.js must check ctrlKey for Windows / Linux users")
	require.Contains(t, body, "metaKey",
		"chat.js must check metaKey for macOS users (Cmd+Enter is the platform's submit gesture)")

	// The handler keys on the Enter key. We assert on the
	// literal token "Enter" — the JS KeyboardEvent.key value
	// for the Enter key is the string "Enter", and any
	// matching handler will use it.
	require.Contains(t, body, "\"Enter\"",
		"chat.js must gate the shortcut on the Enter key")

	// The handler must explicitly preventDefault when it
	// fires the shortcut — without that, the browser still
	// inserts a newline before the form submit, which leaves
	// the textarea in an inconsistent state.
	require.Contains(t, body, "preventDefault",
		"chat.js must call preventDefault when firing the submit shortcut, otherwise the browser inserts a stray newline")
}

// TestChatLanding_PlaceholderHintsKeyboardShortcut pins that
// the chat landing page surfaces the new keyboard shortcut
// to the operator. Without a visible hint, a user who lands
// on the page has no way to know Ctrl+Enter submits — they
// either default to clicking Send or give up on the chat
// entirely.
//
// The test pins the substantive content ("Enter" plus "send")
// rather than the exact wording so the designer can iterate
// on phrasing without breaking the contract.
//
// Phase 2f of the Bulma -> DaisyUI migration: the hint
// paragraph's class moved from Bulma's `help` to daisyUI's
// `label` (the daisyUI label component is the daisyUI
// equivalent of Bulma's help-text styling). The test
// regex pins the substantive content, not the wrapping
// class.
func TestChatLanding_PlaceholderHintsKeyboardShortcut(t *testing.T) {
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

	// A help hint near the textarea. Don't pin the exact
	// class name (was Bulma's `help`, now daisyUI's
	// `label`); pin that a paragraph mentions the Enter
	// key and the submit verb so a future "let's redesign
	// the hint" pass doesn't quietly lose the discovery
	// affordance.
	assert.Regexp(t, `(?i)<p[^>]*>[^<]*[Ee]nter[^<]*[Ss]end[^<]*</p>`, body,
		"the chat form must include a paragraph that names Enter as the submit key")
}
