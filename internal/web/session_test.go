package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionStore_NewSetsIDAndExpiry(t *testing.T) {
	store := newSessionStore(1 * time.Hour)

	s := store.New()

	assert.NotEmpty(t, s.ID)
	assert.Len(t, s.ID, 64) // 32 bytes hex-encoded
	assert.True(t, s.ExpiresAt.After(time.Now()))
	assert.True(t, s.ExpiresAt.Before(time.Now().Add(2*time.Hour)))
}

func TestSessionStore_NewIDsAreUnique(t *testing.T) {
	store := newSessionStore(1 * time.Hour)

	const n = 100
	seen := make(map[string]bool, n)
	for range n {
		s := store.New()
		require.NotEmpty(t, s.ID)
		assert.False(t, seen[s.ID], "duplicate session id %q", s.ID)
		seen[s.ID] = true
	}
}

func TestSessionStore_PutGetRoundTrip(t *testing.T) {
	store := newSessionStore(1 * time.Hour)
	s := store.New()
	s.Subject = "alice"
	s.Username = "alice"
	s.Email = "alice@example.com"
	s.ProviderName = "github"
	store.Put(s)

	got, ok := store.Get(s.ID)
	require.True(t, ok)
	assert.Equal(t, s.ID, got.ID)
	assert.Equal(t, "alice", got.Subject)
	assert.Equal(t, "github", got.ProviderName)
}

func TestSessionStore_GetMissingReturnsFalse(t *testing.T) {
	store := newSessionStore(1 * time.Hour)

	_, ok := store.Get("does-not-exist")

	assert.False(t, ok)
}

func TestSessionStore_GetExpiredReturnsFalseAndEvicts(t *testing.T) {
	store := newSessionStore(50 * time.Millisecond)
	s := store.New()
	store.Put(s)

	// Wait past the TTL so the entry is expired by the time
	// we look it up. Generous margin to absorb CI scheduling
	// jitter.
	time.Sleep(150 * time.Millisecond)

	_, ok := store.Get(s.ID)

	assert.False(t, ok)
	// Get should have evicted the expired entry.
	assert.Equal(t, 0, store.Count())
}

func TestSessionStore_DeleteRemovesEntry(t *testing.T) {
	store := newSessionStore(1 * time.Hour)
	s := store.New()
	store.Put(s)

	store.Delete(s.ID)

	_, ok := store.Get(s.ID)
	assert.False(t, ok)
	assert.Equal(t, 0, store.Count())
}

func TestSessionStore_SweepRemovesOnlyExpired(t *testing.T) {
	store := newSessionStore(1 * time.Hour)
	fresh := store.New()
	fresh.Subject = "fresh"
	store.Put(fresh)

	expired := &Session{
		ID:        "expired-id",
		CreatedAt: time.Now().Add(-2 * time.Hour),
		ExpiresAt: time.Now().Add(-1 * time.Hour),
		Subject:   "expired",
	}
	store.Put(expired)

	removed := store.Sweep()

	assert.Equal(t, 1, removed)
	got, ok := store.Get(fresh.ID)
	require.True(t, ok)
	assert.Equal(t, "fresh", got.Subject)
}

func TestSessionStore_ConcurrentPutGetIsSafe(t *testing.T) {
	store := newSessionStore(1 * time.Hour)

	var wg sync.WaitGroup

	const writers = 4
	const perWriter = 50

	for range writers {
		wg.Go(func() {
			for range perWriter {
				s := store.New()
				s.Subject = "user"
				store.Put(s)
				_, _ = store.Get(s.ID)
			}
		})
	}

	wg.Wait()

	// After concurrent writers, we should have exactly writers*perWriter
	// entries (no torn writes, no lost updates).
	assert.Equal(t, writers*perWriter, store.Count())
}

func TestSetSessionCookie_AttachesHttpOnlyLaxCookie(t *testing.T) {
	w := httptest.NewRecorder()

	// Local-dev install: empty public URL → Secure=false.
	setSessionCookie(w, "my-session", "abc123", "", time.Hour)

	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	c := cookies[0]
	assert.Equal(t, "my-session", c.Name)
	assert.Equal(t, "abc123", c.Value)
	assert.Equal(t, "/", c.Path)
	assert.True(t, c.HttpOnly)
	assert.Equal(t, http.SameSiteLaxMode, c.SameSite)
	assert.Equal(t, int(time.Hour.Seconds()), c.MaxAge)
	// Secure is not asserted here — it depends on whether
	// the configured public URL is https://. Tested
	// separately below.
}

func TestSetSessionCookie_SecureFlagHonored(t *testing.T) {
	w := httptest.NewRecorder()

	setSessionCookie(w, "my-session", "abc", "https://ragabast.example.com", time.Hour)

	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.True(t, cookies[0].Secure)
}

func TestSetSessionCookie_SecureFlagOffForHTTPPublicURL(t *testing.T) {
	w := httptest.NewRecorder()

	// Public URL is http:// — typical for local-dev
	// installs where the operator runs ragabast directly
	// without a proxy. Secure=false so the browser
	// actually persists the cookie over plain HTTP.
	setSessionCookie(w, "my-session", "abc", "http://localhost:8080", time.Hour)

	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.False(t, cookies[0].Secure)
}

func TestSetSessionCookie_SecureFlagOffWhenPublicURLEmpty(t *testing.T) {
	// Defensive: empty string means "no public URL
	// configured" → fall back to Secure=false (historical
	// behavior for local-dev installs that never set
	// server.public_url).
	w := httptest.NewRecorder()

	setSessionCookie(w, "my-session", "abc", "", time.Hour)

	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.False(t, cookies[0].Secure)
}

func TestSetSessionCookie_SecureFlagHandlesTrailingSlash(t *testing.T) {
	// Trailing slash is harmless — the scheme check only
	// looks at the prefix.
	w := httptest.NewRecorder()

	setSessionCookie(w, "my-session", "abc", "https://ragabast.example.com/", time.Hour)

	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.True(t, cookies[0].Secure)
}

func TestSetSessionCookie_SecureFlagIsCaseInsensitive(t *testing.T) {
	// Mixed-case scheme is fine — RFC 3986 says schemes
	// are case-insensitive, and operators occasionally
	// write HTTPS:// in configs.
	w := httptest.NewRecorder()

	setSessionCookie(w, "my-session", "abc", "HTTPS://ragabast.example.com", time.Hour)

	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.True(t, cookies[0].Secure)
}

func TestClearSessionCookie_ZeroesValueAndMaxAge(t *testing.T) {
	w := httptest.NewRecorder()

	clearSessionCookie(w, "my-session", "")

	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	c := cookies[0]
	assert.Equal(t, "my-session", c.Name)
	assert.Empty(t, c.Value)
	assert.Equal(t, -1, c.MaxAge)
	assert.True(t, c.HttpOnly)
}

func TestReadSessionCookie_ReturnsValueWhenPresent(t *testing.T) {
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: "my-session", Value: "abc123"})

	assert.Equal(t, "abc123", readSessionCookie(r, "my-session"))
}

func TestReadSessionCookie_ReturnsEmptyWhenAbsent(t *testing.T) {
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)

	assert.Empty(t, readSessionCookie(r, "my-session"))
}

func TestWithSession_RoundTripsThroughContext(t *testing.T) {
	store := newSessionStore(time.Hour)
	s := store.New()

	ctx := WithSession(context.Background(), s)
	got := SessionFromContext(ctx)

	require.NotNil(t, got)
	assert.Equal(t, s.ID, got.ID)
}

func TestSessionFromContext_ReturnsNilWhenAbsent(t *testing.T) {
	got := SessionFromContext(context.Background())

	assert.Nil(t, got)
}

func TestUserFromSession_BuildsDisplayLabel(t *testing.T) {
	s := &Session{Username: "alice", Email: "alice@example.com", ProviderName: "github"}

	assert.Equal(t, "alice (github)", userFromSession(s).DisplayLabel())
}

func TestSecureFromServerBase(t *testing.T) {
	t.Run("https public URL is secure", func(t *testing.T) {
		assert.True(t, secureFromServerBase("https://ragabast.example.com"))
	})
	t.Run("http public URL is not secure", func(t *testing.T) {
		assert.False(t, secureFromServerBase("http://localhost:8080"))
	})
	t.Run("empty public URL falls back to insecure (local-dev default)", func(t *testing.T) {
		assert.False(t, secureFromServerBase(""))
	})
	t.Run("HTTPS mixed-case is secure", func(t *testing.T) {
		// RFC 3986: schemes are case-insensitive. The
		// check is too.
		assert.True(t, secureFromServerBase("HTTPS://ragabast.example.com"))
	})
	t.Run("trailing slash is harmless", func(t *testing.T) {
		assert.True(t, secureFromServerBase("https://ragabast.example.com/"))
	})
	t.Run("whitespace is trimmed", func(t *testing.T) {
		// Shell expansion or copy-paste sometimes leaves
		// whitespace around the URL. The check tolerates it.
		assert.True(t, secureFromServerBase("  https://ragabast.example.com  "))
	})
}
