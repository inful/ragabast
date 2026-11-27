package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// WebIngestConfig holds the operator's policy for mirroring
// external web pages into the corpus.
//
// Two halves, two owners:
//
//   - The REQUEST comes from a document: its `web_sources:`
//     frontmatter names the pages the author wants mirrored. The
//     request is scattered across the corpus by design — docbuilder
//     aggregates many projects, so there is no single list.
//   - The ALLOWANCE is here: the hosts ragabast may reach. A
//     document can only ever cause a fetch of a host on this list.
//
// The effective set is the intersection. A document that names a
// host the operator did not allow is skipped with a warning — the
// document still ingests, the mirror simply does not exist.
//
// An empty AllowedHosts means the feature is off: ragabast never
// fetches anything.
type WebIngestConfig struct {
	// AllowedHosts are the hosts ragabast may fetch from, as
	// "host" or "host:port". A bare host allows any port on it.
	// Wildcards are not supported: the list is meant to be read
	// as "these hosts, exactly".
	AllowedHosts []string `env:"WEB_INGEST_ALLOWED_HOSTS" yaml:"allowed_hosts,omitempty"`

	// RefreshInterval is how often a mirror is re-fetched to see
	// whether the page changed. Zero means "fetch once" — right
	// for a stable page like a specification, wrong for a
	// changelog.
	RefreshInterval time.Duration `env:"WEB_INGEST_REFRESH_INTERVAL" yaml:"refresh_interval,omitempty"`

	// Timeout bounds a single fetch. Zero means 15s.
	Timeout time.Duration `env:"WEB_INGEST_TIMEOUT" yaml:"timeout,omitempty"`

	// MaxBytes caps a fetched page. Zero means 4 MiB.
	MaxBytes int `env:"WEB_INGEST_MAX_BYTES" yaml:"max_bytes,omitempty"`

	// MaxMirrors caps how many mirrored pages the corpus may hold.
	// Zero means 200. The cap bounds the cost of a corpus that
	// requests many pages.
	MaxMirrors int `env:"WEB_INGEST_MAX_MIRRORS" yaml:"max_mirrors,omitempty"`
}

// Defaults for the zero-value policy.
const (
	WebIngestDefaultTimeout      = 15 * time.Second
	WebIngestDefaultMaxBytes     = 4 << 20 // 4 MiB
	WebIngestDefaultMaxMirrors   = 200
	webIngestDefaultRefreshDelay = time.Hour
)

// IsHostAllowed reports whether the policy permits fetching from
// the given host (as it appears in a URL).
//
// The comparison is on the host[:port] pair, so an entry of
// "example.com" allows "example.com" and "example.com:8080",
// while an entry of "example.com:8080" allows only that port.
func (c *WebIngestConfig) IsHostAllowed(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	hostOnly, _, _ := strings.Cut(host, ":")

	for _, allowed := range c.AllowedHosts {
		allowed = strings.ToLower(strings.TrimSpace(allowed))
		if allowed == "" {
			continue
		}
		allowedHost, allowedPort, hasPort := strings.Cut(allowed, ":")
		if allowedHost != hostOnly {
			continue
		}
		if !hasPort {
			// A bare host allows any port.
			return true
		}
		if _, port, _ := strings.Cut(host, ":"); port == allowedPort {
			return true
		}
	}
	return false
}

// IsURLAllowed reports whether the policy permits fetching the
// given URL. The scheme must be http or https and the host must be
// on the allowlist.
func (c *WebIngestConfig) IsURLAllowed(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	return c.IsHostAllowed(parsed.Host)
}

// EffectiveTimeout returns the fetch timeout, applying the default.
func (c *WebIngestConfig) EffectiveTimeout() time.Duration {
	if c.Timeout <= 0 {
		return WebIngestDefaultTimeout
	}
	return c.Timeout
}

// EffectiveMaxBytes returns the response cap, applying the default.
func (c *WebIngestConfig) EffectiveMaxBytes() int64 {
	if c.MaxBytes <= 0 {
		return WebIngestDefaultMaxBytes
	}
	return int64(c.MaxBytes)
}

// EffectiveMaxMirrors returns the mirror cap, applying the default.
func (c *WebIngestConfig) EffectiveMaxMirrors() int {
	if c.MaxMirrors <= 0 {
		return WebIngestDefaultMaxMirrors
	}
	return c.MaxMirrors
}

// Validate checks the policy for obvious mistakes. An empty
// allowlist is valid: the feature is simply off.
func (c *WebIngestConfig) Validate() error {
	var errs []string

	if c.Timeout < 0 {
		errs = append(errs, "web_ingest.timeout must be >= 0")
	}
	if c.RefreshInterval < 0 {
		errs = append(errs, "web_ingest.refresh_interval must be >= 0")
	}
	if c.MaxBytes < 0 {
		errs = append(errs, "web_ingest.max_bytes must be >= 0")
	}
	if c.MaxMirrors < 0 {
		errs = append(errs, "web_ingest.max_mirrors must be >= 0")
	}
	for _, host := range c.AllowedHosts {
		trimmed := strings.TrimSpace(host)
		if trimmed == "" {
			errs = append(errs, "web_ingest.allowed_hosts must not contain empty entries")
			continue
		}
		if strings.ContainsAny(trimmed, " /?#") {
			errs = append(errs, fmt.Sprintf(
				"web_ingest.allowed_hosts entry %q must be a host (optionally with a port), not a URL or a path", trimmed))
		}
	}

	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("invalid web_ingest config: %s", strings.Join(errs, "; "))
}
