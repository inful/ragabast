package config

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ServerConfig holds web server configuration.
type ServerConfig struct {
	// Address to bind the server.
	Address string `env:"SERVER_ADDRESS" yaml:"address"`

	// Port to bind the server.
	Port int `env:"SERVER_PORT" yaml:"port"`

	// PublicURL is the externally-reachable origin
	// (scheme + host [+ port]) the IdP redirects back
	// to after a successful OAuth login. When set, it
	// overrides the URL derived from Address + Port —
	// required for any install behind a reverse proxy
	// (Traefik / nginx / Caddy / an L7 cloud LB) where
	// the bind address is a private hostname but the
	// public URL is something else entirely.
	//
	// The callback URL the IdP needs to whitelist is
	// `<PublicURL>/auth/<provider-name>/callback` for
	// every configured provider. deriveServerBase
	// trims any trailing slash so concatenation never
	// produces a double slash.
	//
	// Must be a fully-qualified http/https URL with a
	// non-empty host when set. Empty (the default)
	// preserves the historical Address+Port
	// derivation, so single-host installs and
	// localhost dev keep working with zero config.
	//
	// Env: SERVER_PUBLIC_URL. YAML: server.public_url.
	PublicURL string `env:"SERVER_PUBLIC_URL" yaml:"public_url,omitempty"`

	// Enable CORS.
	EnableCORS bool `env:"SERVER_ENABLE_CORS" yaml:"enable_cors"`

	// CORSOrigins is the explicit allow-list of origins the
	// server will echo in Access-Control-Allow-Origin. Wildcard
	// "*" is supported for trusted local-only deployments but
	// is NOT the default — leaving it unset (empty slice)
	// disables cross-origin browser requests entirely, which
	// is the safe default for a single-tenant tool exposed on
	// 127.0.0.1.
	CORSOrigins []string `env:"SERVER_CORS_ORIGINS" yaml:"cors_origins,omitempty"`

	// AuthToken is the shared bearer-token secret the web
	// layer requires on every state-changing or read endpoint
	// when it is non-empty. Empty (the default) keeps the
	// server open so local single-user installs work without
	// configuration. Operators exposing ragabast on a
	// non-loopback interface MUST set this — without it, any
	// network-reachable client can ingest, query, and delete
	// documents.
	//
	// Wire format: clients send `Authorization: Bearer <token>`.
	// Comparison uses constant-time equality (subtle.ConstantTimeCompare)
	// so the endpoint cannot be used as a timing oracle.
	//
	// Kept for backward compatibility; operators running a
	// fleet of internal consumers should prefer AuthTokens
	// (the modern list form) which supports rotation without
	// coordinated outages and per-consumer attribution
	// labels. EffectiveAuthTokens() merges both forms with
	// dedup so an existing single-token config continues to
	// work after the operator adds AuthTokens.
	AuthToken string `env:"SERVER_AUTH_TOKEN" yaml:"auth_token,omitempty"`

	// AuthTokens is the modern list of accepted bearer
	// tokens. Each entry may be a bare string (the common
	// case — no label) or a {value, label} struct when the
	// operator wants per-consumer attribution in the access
	// log. Label has no security meaning — it is purely
	// operational attribution.
	//
	// Token rotation: list both old and new during a grace
	// window, then remove the old once all consumers have
	// migrated. EffectiveAuthTokens handles dedup so adding
	// the same token to both this list and AuthToken (the
	// singular form) does not double-count.
	AuthTokens []AuthToken `env:"SERVER_AUTH_TOKENS" yaml:"auth_tokens,omitempty"`

	// RateLimitPerMinute is the per-client-IP token-bucket
	// rate applied to the LLM-backed endpoints. Zero
	// disables the limiter (the local-dev default). The
	// bucket is refilled continuously; the effective
	// sustained rate is the configured value.
	RateLimitPerMinute int `env:"SERVER_RATE_LIMIT_PER_MINUTE" yaml:"rate_limit_per_minute,omitempty"`

	// RateLimitBurst is the maximum number of immediate
	// requests a single client IP can issue before the
	// per-minute rate kicks in. Defaults to 5; raise it
	// only if the operator's browser interaction trips
	// the limiter, not for "tolerance" reasons.
	RateLimitBurst int `env:"SERVER_RATE_LIMIT_BURST" yaml:"rate_limit_burst,omitempty"`

	// MaxIngestDocumentBytes caps the size of a single
	// document accepted by the ingest endpoints. The 10 MiB
	// request-body limit (H-2) protects the server from
	// memory exhaustion in general, but a single ingest
	// request is also one document — which would still pin
	// the embedding model and OOM the chunker. The default
	// (1 MiB) is generous for any plausible single
	// docbuilder document; operators working with unusually
	// large source documents can raise it.
	MaxIngestDocumentBytes int `env:"SERVER_MAX_INGEST_DOCUMENT_BYTES" yaml:"max_ingest_document_bytes,omitempty"`

	// AsyncIngestQueueDir is the on-disk directory the async
	// ingest queue persists jobs to. Empty disables async
	// ingest (the /api/ingest/async endpoint returns 503).
	// Default: <data_dir>/jobs. The directory is created
	// with mode 0700 at server start; each job file is 0600.
	AsyncIngestQueueDir string `env:"SERVER_ASYNC_INGEST_QUEUE_DIR" yaml:"async_ingest_queue_dir,omitempty"`

	// AsyncIngestWorkers caps the worker-pool concurrency
	// for the async ingest queue. 0 falls back to 1.
	// Default 5 — matches the historical IngestLimiter
	// concurrency for the sync path; raise it to match the
	// embeddings server's request parallelism.
	AsyncIngestWorkers int `env:"SERVER_ASYNC_INGEST_WORKERS" yaml:"async_ingest_workers,omitempty"`

	// AsyncIngestCompletedJobTTL is the maximum age of a
	// completed .json file before the cleanup sweep
	// removes it. Default 168h (7 days). Set to 0 to
	// disable cleanup entirely.
	AsyncIngestCompletedJobTTL time.Duration `env:"SERVER_ASYNC_INGEST_COMPLETED_JOB_TTL" yaml:"async_ingest_completed_job_ttl,omitempty"`

	// AsyncIngestFailedJobTTL is the maximum age of a failed
	// .json file before the cleanup sweep removes it.
	// Default 720h (30 days) — failed jobs default to longer
	// retention than completed ones so operators have time
	// to investigate. Set to 0 to disable.
	AsyncIngestFailedJobTTL time.Duration `env:"SERVER_ASYNC_INGEST_FAILED_JOB_TTL" yaml:"async_ingest_failed_job_ttl,omitempty"`

	// AsyncIngestCleanupInterval is how often the cleanup
	// sweep runs in the background. Default 1h. Set to 0
	// to disable the background sweep (you can still call
	// RunCleanup manually).
	AsyncIngestCleanupInterval time.Duration `env:"SERVER_ASYNC_INGEST_CLEANUP_INTERVAL" yaml:"async_ingest_cleanup_interval,omitempty"`

	// ReadTimeout for HTTP requests.
	ReadTimeout time.Duration `env:"SERVER_READ_TIMEOUT" yaml:"read_timeout"`

	// WriteTimeout for HTTP responses.
	WriteTimeout time.Duration `env:"SERVER_WRITE_TIMEOUT" yaml:"write_timeout"`

	// MCPHTTPEnabled mounts the Model Context Protocol
	// streamable-HTTP server at /mcp on the same listen
	// port. Bearer-token auth via the existing
	// server.auth_tokens applies. Off by default — opt
	// in by setting mcp_http_enabled: true or
	// SERVER_MCP_HTTP_ENABLED=true.
	MCPHTTPEnabled bool `env:"SERVER_MCP_HTTP_ENABLED" yaml:"mcp_http_enabled,omitempty"`
}

// ListenAddr returns an address suitable for net/http Server.Addr.
//
// Rules:
// - If Address already includes a port (e.g. ":8080" or "0.0.0.0:8080"), it is returned as-is.
// - Otherwise Address is treated as a host and combined with Port.
// - If Port is 0, 8080 is used.
//
// Note: Port range validation (1-65535) is enforced by Config.Validate().
func (s ServerConfig) ListenAddr() string {
	addr := strings.TrimSpace(s.Address)
	port := s.Port
	if port == 0 {
		port = 8080
	}

	if addr == "" {
		addr = "0.0.0.0"
	}

	// If addr already includes a port, keep it.
	if _, _, err := net.SplitHostPort(addr); err == nil {
		return addr
	}

	return net.JoinHostPort(addr, strconv.Itoa(port))
}

// AuthToken is one configured bearer credential. Value is
// the opaque secret compared (constant-time) against the
// Authorization header; Label is optional human-readable
// attribution surfaced in the access log under auth_label.
//
// Label has no security meaning — it does not grant any
// privilege, and two tokens may share a label. A blank
// label is allowed.
//
// UnmarshalYAML accepts both the bare-string form
// (`auth_tokens: ["secret-a", "secret-b"]`) and the
// explicit value+label form (`auth_tokens: [{value: ...,
// label: ...}]`) so operators can mix per-token in the
// same list — useful during a rotation grace window where
// new tokens are labeled and old ones aren't.
type AuthToken struct {
	Value string `yaml:"value,omitempty"`
	Label string `yaml:"label,omitempty"`
}

// UnmarshalYAML accepts both the scalar form (`"secret"`) and
// the mapping form (`{value: "secret", label: "x"}`) for
// AuthToken. Operators are expected to mix both freely; this
// is the documented behavior in the config docs.
func (t *AuthToken) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		t.Value = node.Value
		return nil
	}
	var r struct {
		Value string `yaml:"value"`
		Label string `yaml:"label"`
	}
	if err := node.Decode(&r); err != nil {
		return fmt.Errorf("auth_token entry: %w", err)
	}
	if r.Value == "" {
		return errors.New("auth_token entry: value is required")
	}
	t.Value = r.Value
	t.Label = r.Label
	return nil
}

// EffectiveAuthTokens returns the union of AuthToken
// (singular, kept for backward compatibility) and
// AuthTokens (the modern list form). Tokens are deduped
// by Value so an operator who lists the same token in
// both places (typically during a rotation grace window)
// gets exactly one match and the constant-time compare
// loop does not double-bill.
func (s ServerConfig) EffectiveAuthTokens() []AuthToken {
	out := make([]AuthToken, 0, len(s.AuthTokens)+1)
	seen := make(map[string]bool, len(out))
	if s.AuthToken != "" {
		out = append(out, AuthToken{Value: s.AuthToken})
		seen[s.AuthToken] = true
	}
	for _, t := range s.AuthTokens {
		if t.Value == "" || seen[t.Value] {
			continue
		}
		out = append(out, t)
		seen[t.Value] = true
	}
	return out
}
