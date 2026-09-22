package web

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

// chatSessionCookieName is the cookie that carries the
// chat session id across page reloads (issue #22).
//
// Cookie is the right tool here — the value is opaque
// (a server-generated UUID) and HttpOnly + SameSite=Lax
// is what every chat-aware application does. localStorage
// would also work but requires client-side JS; cookies
// just work with the form-submit model that the chat
// page already uses.
const chatSessionCookieName = "ragabast_chat_session"

// chatSessionIDFromRequest returns the chat session id
// from the cookie, or "" when none is set. The caller
// (chat page handler) generates a new id, stashes it in
// a Set-Cookie response, and embeds it in the page form
// so subsequent POSTs round-trip the same value.
func chatSessionIDFromRequest(r *http.Request) string {
	c, err := r.Cookie(chatSessionCookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// ensureChatSessionID returns the existing session id (if
// any) or generates a fresh UUID and tells the caller to
// set the cookie via the returned (id, shouldSet) pair.
// shouldSet=true means the caller must add a Set-Cookie
// header on the response.
func ensureChatSessionID(r *http.Request) (id string, shouldSet bool) {
	if id := chatSessionIDFromRequest(r); id != "" {
		return id, false
	}
	return newChatSessionID(), true
}

// newChatSessionID returns a fresh UUID-shaped identifier
// for use as a chat session key. crypto/rand for
// unpredictability (operators can't enumerate session
// ids from outside) — 16 bytes hex-encoded is the same
// length as a stdlib UUID and unique enough for the
// single-tenant posture.
func newChatSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand.Read on Linux/macOS/Windows only
		// fails when the OS RNG itself is broken. Treat
		// that as catastrophic — better to crash than
		// to silently serve a predictable session id.
		panic("chat session: crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

// setChatSessionCookie writes the session id cookie on
// the response. Cookie is HttpOnly (no JS read), SameSite=Lax
// (top-level navigations work, cross-site form POSTs
// don't), and has a 30-day Max-Age — long enough for
// ongoing conversations, short enough that abandoned
// browsers clear out eventually. The session store
// itself has MaxTurns cap as a tighter bound.
func setChatSessionCookie(w http.ResponseWriter, id string) {
	http.SetCookie(w, &http.Cookie{
		Name:     chatSessionCookieName,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   30 * 24 * 60 * 60, // 30 days
	})
}

// clearChatSessionCookie tells the browser to drop the
// session id cookie. Used by the /chat/clear handler —
// after clearing the cookie, the next chat request has
// no session and starts fresh.
func clearChatSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     chatSessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1, // delete now
	})
}
