package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Session is one authenticated browser session. The struct
// carries everything the auth middleware needs to attribute
// the request, plus the metadata the /auth/me endpoint
// surfaces back to the UI.
//
// Lifecycle: created by the OAuth callback handler after a
// successful IdP round-trip. Stored in sessionStore. Removed
// when the session expires (lazy eviction on Get + Sweep)
// or when the user clicks "log out".
//
// Concurrency: Sessions are immutable once stored. Updates
// (sliding-renewal expiry extension) write a fresh struct
// and Put() overwrites — the map entry is replaced atomically
// under the store's RWMutex.
type Session struct {
	// ID is the 32-byte random identifier hex-encoded into
	// 64 ASCII characters. It is the value stored in the
	// session cookie; the server-side record is keyed by it.
	// Brute-force resistance: 2^255 attempts on average to
	// guess a live ID.
	ID string

	// CreatedAt is when the session was minted (post-IdP
	// callback). Used for audit / display; not for expiry
	// (ExpiresAt is).
	CreatedAt time.Time

	// ExpiresAt is the absolute deadline after which the
	// session is invalid. Sliding-renewal middleware
	// extends this on every authenticated request when
	// the session TTL is non-zero.
	ExpiresAt time.Time

	// Subject is the IdP's stable user identifier — the
	// `sub` claim for OIDC, the user id for GitHub/GitLab.
	// Used to attribute audit log lines; not a grant of
	// privilege on its own.
	Subject string

	// Username is the provider's login/username field
	// (e.g. "alice" on GitHub, "alice" on GitLab). For
	// OIDC, it's the `preferred_username` claim when
	// present, else the local part of the email.
	Username string

	// Email is the provider's primary email. May be empty
	// when the scope set did not request `email` /
	// `user:email`.
	Email string

	// Name is the provider's display name (full name on
	// GitHub, "name" claim on OIDC). May be empty.
	Name string

	// ProviderName is the slug of the OAuth provider that
	// minted the session (matches OAuthProvider.Name in
	// the config). Used by the access log's auth_label
	// and by /auth/me so the UI can show "Signed in via
	// GitHub".
	ProviderName string

	// Role is the role granted to this user — the
	// DefaultRole from OAuthProvider unless a future
	// role-map feature overrides. Today only "user" is
	// used; the field exists so the role-map work
	// doesn't have to touch this struct.
	Role string

	// AllowedUsersHit is true when the IdP returned a
	// user that was on the provider's AllowedUsers list.
	// Stored so /auth/me can explain the source of the
	// grant ("allowed user 'alice'") to the operator.
	AllowedUsersHit bool

	// AllowedUsersMatched is the matched entry in
	// AllowedUsers, when AllowedUsersHit is true.
	AllowedUsersMatched string
}

// User is the UI-side projection of a Session: just the
// fields the /auth/me endpoint and the login-page
// "signed in as" line care about. Keeping this separate
// from Session makes it easy to expand the projection
// (gravatar URL, last-login timestamp, etc.) without
// growing the session record.
type User struct {
	Subject      string
	Username     string
	Email        string
	Name         string
	ProviderName string
	Role         string
}

// DisplayLabel returns a human-readable "who am I" string
// for the navigation bar / page header. Falls back to
// the email or subject when no username is set.
func (u User) DisplayLabel() string {
	if u.Username != "" {
		return u.Username + " (" + u.ProviderName + ")"
	}
	if u.Email != "" {
		return u.Email + " (" + u.ProviderName + ")"
	}

	return u.Subject + " (" + u.ProviderName + ")"
}

// userFromSession projects a Session into the UI-side
// User shape. Returns an empty User when s is nil so
// handlers can write `userFromSession(SessionFromContext(ctx))`
// without a separate nil check.
func userFromSession(s *Session) User {
	if s == nil {
		return User{}
	}

	return User{
		Subject:      s.Subject,
		Username:     s.Username,
		Email:        s.Email,
		Name:         s.Name,
		ProviderName: s.ProviderName,
		Role:         s.Role,
	}
}

// sessionStore holds the live Session records keyed by
// Session.ID. In-memory only — server restart wipes every
// session, which is the documented behavior for the
// in-memory-only storage mode the operator chose.
//
// Concurrency: a sync.RWMutex guards the map. Get runs
// under RLock and may be called on every authenticated
// request; Put / Delete / Sweep take the write lock.
//
// Eviction: Get evicts the entry lazily when it finds an
// expired record; Sweep walks the map and removes all
// expired entries in one pass (for periodic cleanup and
// for the optional "users gc" admin command).
type sessionStore struct {
	mu         sync.RWMutex
	entries    map[string]*Session
	defaultTTL time.Duration
}

// newSessionStore returns an empty store. defaultTTL is
// applied to New() (and to Put when the caller doesn't set
// ExpiresAt explicitly).
func newSessionStore(defaultTTL time.Duration) *sessionStore {
	return &sessionStore{
		entries:    make(map[string]*Session),
		defaultTTL: defaultTTL,
	}
}

// New mints a fresh Session with a random ID and an
// ExpiresAt = now + defaultTTL. The ID is 32 random bytes
// hex-encoded for 64 ASCII characters — far past the
// brute-force threshold.
//
// Panics if crypto/rand.Read fails (the OS RNG is broken).
// That is a deliberate fail-fast: a session id of zeros is
// worthless and we'd rather crash than hand out a known-
// guessable credential.
func (s *sessionStore) New() *Session {
	now := time.Now()
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic("session: crypto/rand failed: " + err.Error())
	}

	return &Session{
		ID:        hex.EncodeToString(raw[:]),
		CreatedAt: now,
		ExpiresAt: now.Add(s.defaultTTL),
	}
}

// Get returns the live Session for id, or false. A found
// but expired entry is evicted as a side-effect of Get
// (under the write lock), so subsequent Get calls do not
// have to re-check the clock.
func (s *sessionStore) Get(id string) (*Session, bool) {
	if id == "" {
		return nil, false
	}

	s.mu.RLock()
	entry, ok := s.entries[id]
	s.mu.RUnlock()

	if !ok {
		return nil, false
	}

	if time.Now().After(entry.ExpiresAt) {
		s.mu.Lock()
		// Re-check under write lock — another goroutine
		// may have just deleted it. delete() on a missing
		// key is a no-op.
		delete(s.entries, id)
		s.mu.Unlock()

		return nil, false
	}

	return entry, true
}

// Put stores (or replaces) the Session under its ID. The
// caller is expected to have set ExpiresAt explicitly;
// Put does NOT silently extend an expired record. Use
// Touch (or Put with a fresh ExpiresAt) for sliding
// renewal.
func (s *sessionStore) Put(session *Session) {
	if session == nil || session.ID == "" {
		return
	}

	s.mu.Lock()
	s.entries[session.ID] = session
	s.mu.Unlock()
}

// Touch replaces session with a copy whose ExpiresAt is
// now + defaultTTL. Sliding-renewal middleware calls this
// on every authenticated request so an actively-used
// session never expires mid-session.
//
// Returns true when the record was found and renewed,
// false when the session was missing (in which case the
// caller's middleware should treat the request as
// unauthenticated). The store does NOT auto-create —
// callers must go through New() + Put() for a fresh
// session.
func (s *sessionStore) Touch(id string) bool {
	if id == "" {
		return false
	}

	s.mu.RLock()
	entry, ok := s.entries[id]
	s.mu.RUnlock()

	if !ok {
		return false
	}

	renewed := *entry
	renewed.ExpiresAt = time.Now().Add(s.defaultTTL)
	s.mu.Lock()
	s.entries[id] = &renewed
	s.mu.Unlock()

	return true
}

// Delete removes the entry under id. Missing ids are
// silently ignored.
func (s *sessionStore) Delete(id string) {
	s.mu.Lock()
	delete(s.entries, id)
	s.mu.Unlock()
}

// Sweep removes every expired entry from the store in one
// pass and returns the number evicted. Callers can run
// this on a timer (every TTL/2 is a reasonable cadence)
// to bound the map's memory footprint.
func (s *sessionStore) Sweep() int {
	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	removed := 0
	for id, entry := range s.entries {
		if now.After(entry.ExpiresAt) {
			delete(s.entries, id)
			removed++
		}
	}

	return removed
}

// Count returns the current number of live (not necessarily
// unexpired) entries. Used by the optional "users" debug
// command and by tests; production code paths don't need
// this.
func (s *sessionStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return len(s.entries)
}

// sessionContextKey is the unexported context key under
// which authMiddleware and friends stash the matched
// Session. Untyped (empty struct) so external packages
// cannot collide.
type sessionContextKey struct{}

// WithSession returns a child context carrying session.
// Handlers and the access-log layer read it via
// SessionFromContext.
func WithSession(ctx context.Context, session *Session) context.Context {
	if session == nil {
		return ctx
	}

	return context.WithValue(ctx, sessionContextKey{}, session)
}

// SessionFromContext returns the Session associated with
// ctx, or nil when the request is unauthenticated.
func SessionFromContext(ctx context.Context) *Session {
	if ctx == nil {
		return nil
	}
	s, _ := ctx.Value(sessionContextKey{}).(*Session)

	return s
}

// UserFromContext is the convenience wrapper that projects
// the session to its UI-side User. Handlers rendering the
// "signed in as" line should call this so the rendering
// layer stays decoupled from the Session struct.
func UserFromContext(ctx context.Context) User {
	return userFromSession(SessionFromContext(ctx))
}

// setSessionCookie writes the session-id cookie with
// HttpOnly + SameSite=Lax. Secure is enabled when the
// configured public origin (server.public_url) uses
// https:// — the Secure flag must match the public
// scheme, not the private backend scheme, so the
// browser actually persists the cookie on the
// user-visible URL. Local-dev installs that leave
// server.public_url empty get Secure=false and the
// browser keeps the cookie over plain HTTP. See
// secureFromServerBase for the full reasoning.
func setSessionCookie(w http.ResponseWriter, name, value, serverBase string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		Secure:   secureFromServerBase(serverBase),
		SameSite: http.SameSiteLaxMode,
	})
}

// clearSessionCookie sets MaxAge=-1 so the browser drops
// the cookie immediately. The value is set to "" so any
// stale path / domain variants are also clobbered.
// serverBase is used the same way as in setSessionCookie
// so the deletion-clearing cookie matches the cookie it
// overwrites (some browsers refuse to overwrite a
// cookie whose attributes don't match).
func clearSessionCookie(w http.ResponseWriter, name, serverBase string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secureFromServerBase(serverBase),
		SameSite: http.SameSiteLaxMode,
	})
}

// readSessionCookie returns the value of the session
// cookie, or "" when it is missing. The caller is
// responsible for passing the configured CookieName so
// an operator who customized the name still gets matched.
func readSessionCookie(r *http.Request, name string) string {
	c, err := r.Cookie(name)
	if err != nil {
		return ""
	}

	return c.Value
}

// secureFromServerBase reports whether the configured
// public origin uses https://. This is the source of
// truth for the Secure flag on auth-related cookies,
// because the Secure flag must match the PUBLIC scheme
// (what the browser sees in the URL bar and uses to
// decide whether to send the cookie), not the private
// backend scheme (what ragabast sees on the wire).
//
// Why not just secureFromRequest? Behind a reverse proxy
// the proxy terminates TLS, so the connection to ragabast
// is HTTP — r.TLS is nil. The X-Forwarded-Proto header
// tells ragabast the public scheme, but a misconfigured
// proxy can forget to forward it on some requests,
// producing an inconsistent Secure flag across the
// login + callback round-trip — exactly the "state cookie
// missing or mismatched" failure mode operators hit when
// running behind Traefik or similar proxies.
//
// serverBase is the operator-explicit server.public_url
// (when set) or the derived http://<address>:<port>
// fallback. When server.public_url is set, that's the
// authority — the proxy's job is to honor it, not to
// override it. When unset, the fallback matches the
// historical insecure-default behavior.
func secureFromServerBase(serverBase string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(serverBase)), "https://")
}
