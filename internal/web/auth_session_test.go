package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ragabast/internal/config"
)

// authMiddlewareWithSession wires a middleware backed by both
// bearer tokens AND a session store. Tests pass nil for
// sessionStore to exercise the bearer-only path that older
// callers rely on.
func authMiddlewareWithSession(tokens []config.AuthToken, store *sessionStore, cookieName string) func(http.Handler) http.Handler {
	return authMiddleware(tokens, store, cookieName)
}

func TestAuthMiddleware_SessionCookieGrantsAccess(t *testing.T) {
	store := newSessionStore(time.Hour)
	sess := store.New()
	sess.Subject = "alice"
	sess.Username = "alice"
	sess.ProviderName = "gh"
	store.Put(sess)

	// No bearer token; just the session cookie. The
	// middleware must accept it on a protected route.
	mw := authMiddlewareWithSession(nil, store, "ragabast_session")
	called := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		// The handler should see the session on the context.
		got := SessionFromContext(r.Context())
		if assert.NotNil(t, got) {
			assert.Equal(t, "alice", got.Username)
			assert.Equal(t, "gh", got.ProviderName)
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/ingest", nil)
	req.AddCookie(&http.Cookie{Name: "ragabast_session", Value: sess.ID})
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	assert.True(t, called, "middleware must invoke handler for valid session")
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAuthMiddleware_RejectsMissingSessionAndBearer(t *testing.T) {
	store := newSessionStore(time.Hour)

	mw := authMiddlewareWithSession(nil, store, "ragabast_session")
	called := false
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/ingest", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.False(t, called, "handler must not be invoked without session or bearer")
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, `Bearer realm="ragabast"`, w.Header().Get("WWW-Authenticate"))
}

func TestAuthMiddleware_RejectsUnknownSessionID(t *testing.T) {
	store := newSessionStore(time.Hour)

	mw := authMiddlewareWithSession(nil, store, "ragabast_session")
	called := false
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/ingest", nil)
	req.AddCookie(&http.Cookie{Name: "ragabast_session", Value: "not-a-real-id"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.False(t, called)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthMiddleware_RejectsExpiredSession(t *testing.T) {
	store := newSessionStore(50 * time.Millisecond)
	sess := store.New()
	store.Put(sess)

	// Wait past the TTL.
	time.Sleep(150 * time.Millisecond)

	mw := authMiddlewareWithSession(nil, store, "ragabast_session")
	called := false
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/ingest", nil)
	req.AddCookie(&http.Cookie{Name: "ragabast_session", Value: sess.ID})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.False(t, called)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthMiddleware_BothBearerAndSession_BearerWins(t *testing.T) {
	// Both credentials present. The bearer path is
	// checked first (matches the API-friendly UX), and the
	// session is left untouched — programs don't need
	// a browser session to use the API.
	tokens := []config.AuthToken{{Value: "good-bearer", Label: "api"}}
	store := newSessionStore(time.Hour)
	sess := store.New()
	sess.Username = "alice"
	sess.ProviderName = "gh"
	store.Put(sess)

	mw := authMiddlewareWithSession(tokens, store, "ragabast_session")
	var label string
	var sessionOnContext *Session
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		label = AuthLabelFromContext(r.Context())
		sessionOnContext = SessionFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/ingest", nil)
	req.Header.Set("Authorization", "Bearer good-bearer")
	req.AddCookie(&http.Cookie{Name: "ragabast_session", Value: sess.ID})
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "api", label, "bearer label wins")
	// No session context when bearer was used — bearer
	// callers are programs, not browsers.
	assert.Nil(t, sessionOnContext)
}

func TestAuthMiddleware_NilSessionStoreFallsBackToBearer(t *testing.T) {
	// Backwards-compat: when no session store is wired
	// (the existing server's path when auth.providers is
	// empty), bearer-token behavior is unchanged.
	tokens := []config.AuthToken{{Value: "good-bearer"}}

	mw := authMiddlewareWithSession(tokens, nil, "")
	called := false
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/ingest", nil)
	req.Header.Set("Authorization", "Bearer good-bearer")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAuthMiddleware_NoAuthConfigured_NoSessionStore_IsNoOp(t *testing.T) {
	// No tokens, no session store → the historical
	// single-user local install keeps working with no
	// configuration.
	mw := authMiddlewareWithSession(nil, nil, "")
	called := false
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/ingest", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.True(t, called, "open access when no auth is configured")
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAuthMiddleware_PublicRoutesBypassEvenWithSession(t *testing.T) {
	// Public GET routes skip auth entirely; session
	// context is still attached when a session cookie is
	// present so the page can render the "signed in as"
	// line.
	store := newSessionStore(time.Hour)
	sess := store.New()
	sess.Username = "alice"
	sess.ProviderName = "gh"
	store.Put(sess)

	mw := authMiddlewareWithSession(nil, store, "ragabast_session")
	var sessionOnContext *Session
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sessionOnContext = SessionFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/chat", nil)
	req.AddCookie(&http.Cookie{Name: "ragabast_session", Value: sess.ID})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NotNil(t, sessionOnContext, "public routes should still see the session when one is present")
	assert.Equal(t, "alice", sessionOnContext.Username)
}

func TestAuthMiddleware_SlidingRenewalOnAccess(t *testing.T) {
	store := newSessionStore(200 * time.Millisecond)
	sess := store.New()
	store.Put(sess)
	originalExpiry := sess.ExpiresAt

	mw := authMiddlewareWithSession(nil, store, "ragabast_session")
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Wait half the TTL so we can detect a renewal
	// (which pushes expiry forward).
	time.Sleep(100 * time.Millisecond)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/ingest", nil)
	req.AddCookie(&http.Cookie{Name: "ragabast_session", Value: sess.ID})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// The session in the store should have been renewed
	// (ExpiresAt moved forward past the original).
	renewed, ok := store.Get(sess.ID)
	assert.True(t, ok)
	assert.True(t, renewed.ExpiresAt.After(originalExpiry), "expected sliding renewal to push expiry forward (was %v, now %v)", originalExpiry, renewed.ExpiresAt)
}

func TestAuthMiddleware_BrowserRedirectsToLoginWhenOAuthEnabled(t *testing.T) {
	// When OAuth is configured and a browser hits a
	// protected route without a session, redirect to
	// /auth/login?next=<path> so the operator lands on
	// the chooser page rather than a raw 401.
	store := newSessionStore(time.Hour)

	mw := authMiddlewareWithSession(nil, store, "ragabast_session")
	called := false
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/ingest", nil)
	req.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.False(t, called)
	assert.Equal(t, http.StatusFound, w.Code)
	loc, err := w.Result().Location()
	require.NoError(t, err)
	assert.Equal(t, "/auth/login?next=%2Fapi%2Fingest", loc.String())
}

func TestAuthMiddleware_BrowserRedirectPreservesQueryString(t *testing.T) {
	store := newSessionStore(time.Hour)
	mw := authMiddlewareWithSession(nil, store, "ragabast_session")
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	// /api/ingest is a protected POST route. Use the
	// query string to verify the redirect URL preserves
	// it intact.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/ingest?doc=abc", nil)
	req.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusFound, w.Code)
	loc, err := w.Result().Location()
	require.NoError(t, err)
	assert.Equal(t, "/auth/login?next=%2Fapi%2Fingest%3Fdoc%3Dabc", loc.String())
}

func TestAuthMiddleware_APIRequestStillGets401WhenOAuthEnabled(t *testing.T) {
	// Programmatic clients (Accept: application/json or
	// no Accept header) still get a 401 with
	// WWW-Authenticate so they can retry correctly.
	store := newSessionStore(time.Hour)
	mw := authMiddlewareWithSession(nil, store, "ragabast_session")
	called := false
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/ingest", nil)
	req.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.False(t, called)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, `Bearer realm="ragabast"`, w.Header().Get("WWW-Authenticate"))
}

func TestAuthMiddleware_NoOAuthConfigured_StillReturns401(t *testing.T) {
	// When only bearer-token auth is configured (no
	// OAuth), the 401 path stays so operators who forgot
	// to set auth_token still see a clear "this is
	// broken" signal — no spurious redirects to a
	// nonexistent /auth/login.
	tokens := []config.AuthToken{{Value: "good-bearer"}}
	mw := authMiddlewareWithSession(tokens, nil, "")
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/ingest", nil)
	req.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, `Bearer realm="ragabast"`, w.Header().Get("WWW-Authenticate"))
}

// mustContext is removed; tests can call r.Context() directly.
