package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ragabast/internal/config"
	"github.com/stretchr/testify/require"
)

// TestRateLimiter_BurstThenThrottle pins the H-4 fix: the
// LLM-backed endpoints (POST /api/query, POST /api/search,
// POST /api/link-suggestions, POST /api/frontmatter/suggest,
// POST /chat/message, POST /search) MUST be rate-limited so
// a single caller cannot spam them and amplify spend on the
// chat provider.
//
// The limiter is keyed by client IP (RemoteAddr), uses a
// token-bucket algorithm, and replies 429 with Retry-After
// once the bucket is empty. The default rate and burst are
// pinned by the test below; the values are deliberately
// generous so legitimate browser interaction (the operator
// type-and-submit loop) never trips the limiter, while a
// hostile scripted client cannot reach the upstream LLM at
// thousands of requests per second.
func TestRateLimiter_BurstThenThrottle(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.RateLimitPerMinute = 60
	cfg.Server.RateLimitBurst = 1
	s := NewServer(cfg, &fakeHumaService{})

	doSearch := func(remoteAddr string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/search", strings.NewReader(`{"query":"a","limit":5,"min_score":0.5}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = remoteAddr
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)
		return w
	}

	w := doSearch("192.0.2.1:1234")
	require.Equal(t, http.StatusOK, w.Code,
		"first request within burst must succeed")
	w = doSearch("192.0.2.1:1234")
	require.Equal(t, http.StatusTooManyRequests, w.Code,
		"second request before refill must be 429")
	require.NotEmpty(t, w.Header().Get("Retry-After"),
		"429 must advertise Retry-After")
}

// TestRateLimiter_PerKeyIsolation confirms the bucket is
// per-client-IP: an attacker hammering from one IP does not
// starve a different IP's bucket.
func TestRateLimiter_PerKeyIsolation(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.RateLimitPerMinute = 60
	cfg.Server.RateLimitBurst = 1
	s := NewServer(cfg, &fakeHumaService{})

	doSearch := func(remoteAddr string) int {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/search", strings.NewReader(`{"query":"a","limit":5,"min_score":0.5}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = remoteAddr
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)
		return w.Code
	}

	require.Equal(t, http.StatusOK, doSearch("192.0.2.1:1234"))
	require.Equal(t, http.StatusTooManyRequests, doSearch("192.0.2.1:1234"))
	require.Equal(t, http.StatusOK, doSearch("192.0.2.2:1234"),
		"a different client IP must not be throttled by IP A's bucket")
}

// TestRateLimiter_OnlyOnLLMEndpoints pins that the limiter is
// applied ONLY to LLM-backed / costly endpoints, not every
// route. A health check from the same IP after the bucket is
// drained must still succeed.
func TestRateLimiter_OnlyOnLLMEndpoints(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.RateLimitPerMinute = 60
	cfg.Server.RateLimitBurst = 1
	s := NewServer(cfg, &fakeHumaService{})

	drainBucket := func() {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/search", strings.NewReader(`{"query":"a","limit":5,"min_score":0.5}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "192.0.2.1:1234"
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)
		_ = w.Code
	}
	drainBucket()
	drainBucket() // bucket empty now

	// Health check from the same IP must still succeed.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/health", nil)
	req.RemoteAddr = "192.0.2.1:1234"
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code,
		"non-LLM endpoint must not count against the LLM bucket")
}

// TestRateLimiter_DisabledWhenZero confirms the limiter is
// disabled when rate is 0 (the local-dev case). Operators who
// want to bypass it entirely (loopback-only installs) get
// that behavior by setting rate_limit_per_minute: 0 in the
// config.
func TestRateLimiter_DisabledWhenZero(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.RateLimitPerMinute = 0 // disabled
	s := NewServer(cfg, &fakeHumaService{})

	doSearch := func() int {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/search", strings.NewReader(`{"query":"a","limit":5,"min_score":0.5}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "192.0.2.1:1234"
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)
		return w.Code
	}

	// Fire 5 requests rapidly; all must succeed.
	for range 5 {
		require.Equal(t, http.StatusOK, doSearch(),
			"with rate=0 the limiter is disabled and all requests pass")
	}
}

// TestSearchForm_TopKClampedServerSide pins the H-5 fix: the
// web form /search handler MUST clamp top_k to a sane upper
// bound even when the client bypasses the HTML <input
// max="50"> constraint (curl, scripted request). The vector
// DB layer's clamp at min(count, 1000) is a defense-in-depth
// upper bound; the handler cap is the security boundary.
func TestSearchForm_TopKClampedServerSide(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeService{})

	cases := []struct {
		name     string
		formTopK string
		wantCap  int
	}{
		{"empty defaults", "", 5},
		{"positive in range", "10", 10},
		{"over the cap is clamped", "1000000", 50},
		{"zero falls back to default", "0", 5},
		{"negative falls back to default", "-1", 5},
		{"non-numeric falls back to default", "abc", 5},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := "query=hello"
			if tc.formTopK != "" {
				body += "&top_k=" + tc.formTopK
			}
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/search", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			s.router.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code)
			require.Equal(t, tc.wantCap, s.service.(*fakeService).lastSearchLimit,
				"form top_k=%q must clamp to %d (handler passed %d)",
				tc.formTopK, tc.wantCap, s.service.(*fakeService).lastSearchLimit)
		})
	}
}
