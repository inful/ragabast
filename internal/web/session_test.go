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

	setSessionCookie(w, "my-session", "abc123", false, time.Hour)

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
	// TLS is detected. Tested separately below.
}

func TestSetSessionCookie_SecureFlagHonored(t *testing.T) {
	w := httptest.NewRecorder()

	setSessionCookie(w, "my-session", "abc", true, time.Hour)

	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.True(t, cookies[0].Secure)
}

func TestClearSessionCookie_ZeroesValueAndMaxAge(t *testing.T) {
	w := httptest.NewRecorder()

	clearSessionCookie(w, "my-session", false)

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

func TestSecureFromRequest(t *testing.T) {
	t.Run("plain http is not secure", func(t *testing.T) {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)

		assert.False(t, secureFromRequest(r))
	})
	t.Run("X-Forwarded-Proto: https is secure", func(t *testing.T) {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		r.Header.Set("X-Forwarded-Proto", "https")

		assert.True(t, secureFromRequest(r))
	})
}
