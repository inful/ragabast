package web

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// llmRateLimiter applies a per-client-IP token-bucket rate
// limit to the LLM-backed endpoints (/api/query, /api/search,
// /api/link-suggestions, /api/frontmatter/suggest, /chat/
// message, /search). Health checks, static stub, and HTML
// form renders are NOT throttled — only the routes that
// spend upstream tokens at the chat provider.
//
// Why per-IP and not per-token: the auth layer is bearer
// token (a single shared secret), so all callers have the
// same identity from the limiter's perspective. Per-IP is
// the only meaningful partition. A more sophisticated scheme
// (per-token, per-user) would require multi-user auth which
// is out of scope for the single-tenant tool.
//
// Configuration:
//   - perMinute (rate): tokens added per second = perMinute / 60.
//     0 disables the limiter (every request passes).
//   - burst: bucket size. Browser interactions issue at most
//     a handful of requests per second; 5 is a comfortable
//     default that a hostile scripted client cannot reach.
//
// Implementation: a sync.Map keyed by client IP, each entry
// holding a *rate.Limiter. The map is intentionally append-
// only; idle entries are pruned opportunistically by a
// background goroutine that wakes every minute. Without the
// prune step an attacker rotating IPs across a /16 could
// grow the map unboundedly.
//
// Why golang.org/x/time/rate: it implements the standard
// token-bucket algorithm in <100 LOC and is the most-used
// rate-limit primitive in the Go ecosystem. Rolling our own
// would add risk for no benefit.
type llmRateLimiter struct {
	perMinute int
	burst     int

	mu       sync.Mutex
	buckets  map[string]*ipBucket
	pruneNow time.Time // last prune time; lazy GC triggers here
}

type ipBucket struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// newLLMRateLimiter constructs a limiter. perMinute <= 0
// returns nil — callers must check for nil and pass through.
func newLLMRateLimiter(perMinute, burst int) *llmRateLimiter {
	if perMinute <= 0 {
		return nil
	}
	if burst <= 0 {
		burst = 5
	}
	l := &llmRateLimiter{
		perMinute: perMinute,
		burst:     burst,
		buckets:   make(map[string]*ipBucket),
		pruneNow:  time.Now(),
	}
	return l
}

// rate returns the configured per-minute rate, normalized to
// the per-second rate that golang.org/x/time/rate expects.
// Exposed for tests so they can assert the bucket math.
func (l *llmRateLimiter) rate() rate.Limit {
	return rate.Limit(float64(l.perMinute) / 60.0)
}

// allow returns true if one token is currently available for
// the given client IP. false means the caller should be
// rejected with 429 + Retry-After.
//
// allow is the only hot-path method on the limiter; it MUST
// stay allocation-free in the steady state (a single map
// lookup + a token decrement).
func (l *llmRateLimiter) allow(ip string) bool {
	if l == nil {
		return true
	}
	now := time.Now()

	l.mu.Lock()
	b, ok := l.buckets[ip]
	if !ok {
		b = &ipBucket{
			limiter: rate.NewLimiter(l.rate(), l.burst),
		}
		l.buckets[ip] = b
	}
	b.lastSeen = now
	limiter := b.limiter
	shouldPrune := now.Sub(l.pruneNow) > time.Minute
	l.mu.Unlock()

	if shouldPrune {
		// Lazy GC: do not block the request on it.
		go l.prune(now)
	}

	return limiter.Allow()
}

// prune removes buckets that have been idle for > 10 minutes.
// 10 minutes is a comfortable window: longer than any
// legitimate browser session and short enough that the map
// cannot grow without bound under attack.
func (l *llmRateLimiter) prune(now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneNow = now
	for ip, b := range l.buckets {
		if now.Sub(b.lastSeen) > 10*time.Minute {
			delete(l.buckets, ip)
		}
	}
}

// retryAfter returns the wall-clock duration the caller
// should wait before retrying. Computed from the bucket's
// reserve (rate.Limiter.Reserve) so the value is honest: it
// is the time the next token would actually become available.
func (l *llmRateLimiter) retryAfter(ip string) time.Duration {
	if l == nil {
		return time.Second
	}
	l.mu.Lock()
	b, ok := l.buckets[ip]
	l.mu.Unlock()
	if !ok {
		return time.Second
	}
	r := b.limiter.Reserve()
	if !r.OK() {
		return time.Second
	}
	delay := r.Delay()
	r.Cancel() // we did not actually consume the token
	if delay <= 0 {
		return time.Second
	}
	return delay
}

// middleware was the un-path-aware version of the throttle.
// Removed in favor of llmPathMiddleware (below), which only
// acts on LLM-backed paths. The blanket-throttle version
// regressed the security review's "non-LLM endpoints must
// not count against the bucket" property.

// clientIP extracts the client IP from the request. Order of
// preference: r.RemoteAddr's host part. chi's middleware.RealIP
// has already populated r.RemoteAddr with the X-Forwarded-For
// value (when behind a trusted proxy), so we just split the
// host:port.
//
// Falls back to a constant when RemoteAddr is empty (it never
// is for real http.Request values, but defensive against
// synthetic test requests). All such requests share the
// "unknown" bucket, which is acceptable for testing.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// RemoteAddr might be just "host" without a port.
		if r.RemoteAddr != "" {
			return r.RemoteAddr
		}
		return "unknown"
	}
	return host
}

// llmPathPrefixes is the set of URL prefixes that count
// against the per-IP token bucket. Anything outside this set
// (health checks, static, HTML form renders, the documents
// list/delete/prune endpoints) is not throttled — only the
// routes that spend upstream tokens or write to the store.
//
// The list mirrors the rate-sensitive endpoints from the
// security review:
//   - LLM-backed endpoints (chat-completions cost):
//     /api/query, /api/search, /api/link-suggestions,
//     /api/frontmatter/suggest, /chat/message (POST),
//     /search (POST).
//   - Ingest endpoints (embedding + write cost):
//     /api/ingest, /api/ingest/raw, /api/ingest/file,
//     /ingest (POST).
//
// Ingest is included because a single caller can saturate
// the 5-slot IngestLimiter forever without per-IP
// throttling — a hostile operator could amplify embedding
// spend by hammering /api/ingest at line rate.
var llmPathPrefixes = []string{
	"/api/query",
	"/api/search",
	"/api/link-suggestions",
	"/api/frontmatter/suggest",
	"/chat/message",
	"/search",
	"/api/ingest",
	"/api/ingest/raw",
	"/api/ingest/file",
	"/ingest",
}

// llmPathMiddleware is the chi-level rate-limit middleware
// applied at the LLM path prefixes above. Returns 429 with
// Retry-After when the bucket is empty; otherwise passes
// through. A nil receiver is a no-op (rate limiting disabled).
//
// The middleware is path-aware: a request whose URL path does
// NOT match any of llmPathPrefixes is passed through
// untouched (no token consumed). A matching request consumes
// one token from the per-IP bucket.
//
// The middleware exists so the same per-IP bucket applies to
// both the Huma-mounted endpoints (whose Huma.Middleware
// interface cannot reach r.RemoteAddr) and the form-mounted
// endpoints (which use router.With(this)). One chi-level
// middleware covers both code paths.
func (l *llmRateLimiter) llmPathMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if l == nil || !isLLMRateLimitedPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		ip := clientIP(r)
		if !l.allow(ip) {
			retry := l.retryAfter(ip)
			secs := max(int(retry.Seconds()+1), 1)
			w.Header().Set("Retry-After", strconv.Itoa(secs))
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isLLMRateLimitedPath reports whether the given URL path is
// one of the LLM-backed endpoints that count against the
// per-IP bucket. Used by llmPathMiddleware to skip
// non-throttled routes (health checks, static, HTML pages).
//
// Method-aware: GET /api/search is NOT throttled (the LLM
// cost lives in the POST handler), POST /search IS. The
// prefix check is path-only — callers that need method-aware
// behavior must check r.Method upstream.
func isLLMRateLimitedPath(path string) bool {
	for _, p := range llmPathPrefixes {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}
