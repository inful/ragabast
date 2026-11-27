package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/webfetch"
)

// webMirrorStateFile is the registry of mirrored pages, stored next
// to the vector store.
//
// The corpus cannot answer "which documents requested this URL?" —
// the request lives in a document's frontmatter, and the vector
// store indexes chunks, not the frontmatter of the parent. So the
// registry keeps that relation: it is what makes the effective list
// (requested ∩ allowed) queryable, what drives the garbage
// collection of mirrors nobody requests anymore, and what tells the
// refresh ticker which mirrors are due.
const webMirrorStateFile = "web_mirrors.json"

// webMirrorUIDPrefix is the UID prefix of a mirrored page. The
// prefix makes mirrors recognizable in the corpus and in the
// documents list (their source kind is "web" too).
const webMirrorUIDPrefix = "web:"

// WebMirror is one mirrored page and its state.
type WebMirror struct {
	// SourceURL is the page that was fetched.
	SourceURL string `json:"source_url"`
	// UID is the mirror document's UID.
	UID string `json:"uid"`
	// DeclaredBy are the UIDs of the documents that requested this
	// page. A mirror with no declarer is garbage.
	DeclaredBy []string `json:"declared_by,omitempty"`
	// FetchedAt is when the page was last fetched.
	FetchedAt time.Time `json:"fetched_at"`
	// ContentHash is the hash of the converted markdown, used to
	// skip a re-embed when the page did not change.
	ContentHash string `json:"content_hash,omitempty"`
	// Title is the page's title, as far as the server reported it.
	Title string `json:"title,omitempty"`
	// Error is the last failure, empty when the last fetch worked.
	Error string `json:"error,omitempty"`
}

// webMirrorRegistry is the on-disk registry.
type webMirrorRegistry struct {
	Mirrors map[string]*WebMirror `json:"mirrors"`
}

// webMirrorState is the service-side registry plus its lock.
type webMirrorState struct {
	mu       sync.Mutex
	registry *webMirrorRegistry
	path     string
}

// mirrorWebSources fetches and ingests the external pages the given
// document requests, and drops the mirrors it requested before but
// no longer does.
//
// The request comes from the document's `web_sources:` frontmatter;
// whether ragabast may fetch it comes from the operator's
// allowlist. A request for a host that is not allowed is skipped
// with a warning — the document still ingests.
//
// Failures are contained: a page that cannot be fetched leaves the
// document's ingest untouched and is reported in the registry.
func (s *Service) mirrorWebSources(ctx context.Context, doc *models.Document) {
	if doc == nil {
		return
	}
	// A mirror never requests mirrors: that would let a fetched
	// page pull in more pages.
	if doc.SourceKind == models.SourceWeb {
		return
	}

	policy := s.config.WebIngest
	state := s.webMirrorState()
	requested := make(map[string]struct{}, len(doc.WebSources))
	for _, raw := range doc.WebSources {
		if trimmed := strings.TrimSpace(raw); trimmed != "" {
			requested[trimmed] = struct{}{}
		}
	}

	// Pages this document requested before but no longer does: the
	// mirror goes away unless another document still wants it. This
	// runs even when the document requests nothing now — that is
	// exactly the case where a mirror has to go.
	for _, orphan := range state.dropDeclarations(doc.UID, requested) {
		if err := s.DeleteDocument(ctx, orphan.UID); err != nil {
			log.Printf("service: cannot remove the orphaned mirror %q: %v", orphan.UID, err)
			continue
		}
		log.Printf("service: removed the mirror %q — no document requests it anymore", orphan.SourceURL)
	}

	if len(requested) == 0 {
		return
	}
	if len(policy.AllowedHosts) == 0 {
		// The feature is off. Say it once per document rather than
		// silently ignoring the request.
		log.Printf("service: document %q requests %d web source(s) but web_ingest.allowed_hosts is empty — not fetching",
			doc.UID, len(requested))
		return
	}

	for raw := range requested {
		if !policy.IsURLAllowed(raw) {
			log.Printf("service: document %q requests %q but the host is not on web_ingest.allowed_hosts — not fetching",
				doc.UID, raw)
			state.setError(raw, "host not allowed")
			continue
		}

		if err := s.mirrorOne(ctx, state, doc.UID, raw); err != nil {
			log.Printf("service: cannot mirror %q for document %q: %v", raw, doc.UID, err)
			state.setError(raw, err.Error())
			continue
		}
	}
}

// WebMirrors returns the mirrored pages and their state: the
// effective list (what was requested and fetched), each entry with
// the documents that requested it and the last fetch outcome.
func (s *Service) WebMirrors() []WebMirror {
	return s.webMirrorState().mirrors()
}

// AllowedWebHosts returns the hosts the operator allows ragabast
// to fetch from. Empty means the mirroring feature is off.
func (s *Service) AllowedWebHosts() []string {
	hosts := s.config.WebIngest.AllowedHosts
	out := make([]string, 0, len(hosts))
	for _, host := range hosts {
		if trimmed := strings.TrimSpace(host); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// webMirrorRefreshTick is how often the refresher looks for
// mirrors that are due. The per-mirror interval decides *whether*
// a mirror is due; this only decides how often that is noticed.
const webMirrorRefreshTick = 15 * time.Minute

// StartWebMirrorRefresher starts the background refresher that
// re-fetches the mirrored pages whose refresh interval has elapsed.
//
// It is a no-op when the interval is zero (mirrors are snapshots)
// or when the allowlist is empty (the feature is off). The caller
// owns the context: canceling it stops the goroutine.
func (s *Service) StartWebMirrorRefresher(ctx context.Context) {
	interval := s.config.WebIngest.RefreshInterval
	if interval <= 0 || len(s.config.WebIngest.AllowedHosts) == 0 {
		return
	}

	go func() {
		ticker := time.NewTicker(webMirrorRefreshTick)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.refreshWebMirrors(ctx, interval)
			}
		}
	}()
}

// refreshWebMirrors re-fetches the mirrors that are due.
//
// A mirror whose host is no longer allowed is removed: the
// allowlist is the operator's authority, and revoking a host takes
// effect on the next cycle.
func (s *Service) refreshWebMirrors(ctx context.Context, interval time.Duration) {
	state := s.webMirrorState()
	policy := s.config.WebIngest

	for _, mirror := range state.mirrors() {
		if !policy.IsURLAllowed(mirror.SourceURL) {
			if err := s.DeleteDocument(ctx, mirror.UID); err != nil {
				log.Printf("service: cannot remove the revoked mirror %q: %v", mirror.UID, err)
				continue
			}
			state.remove(mirror.SourceURL)
			log.Printf("service: removed the mirror %q — its host is no longer on web_ingest.allowed_hosts",
				mirror.SourceURL)
			continue
		}

		if !mirror.FetchedAt.IsZero() && time.Since(mirror.FetchedAt) < interval {
			continue
		}

		if err := s.mirrorOne(ctx, state, mirror.DeclaredBy[0], mirror.SourceURL); err != nil {
			log.Printf("service: cannot refresh the mirror %q: %v", mirror.SourceURL, err)
			state.setError(mirror.SourceURL, err.Error())
			continue
		}
	}
}

// mirrorOne fetches a page and ingests it as a mirror document.
func (s *Service) mirrorOne(ctx context.Context, state *webMirrorState, declaredBy, rawURL string) error {
	policy := s.config.WebIngest

	if state.count() >= policy.EffectiveMaxMirrors() {
		return fmt.Errorf("the mirror cap (%d) is reached", policy.EffectiveMaxMirrors())
	}

	fetch := s.fetchWebPage
	if fetch == nil {
		fetch = webfetch.Fetch
	}
	result, err := fetch(ctx, rawURL, webfetch.Policy{
		Timeout:  policy.EffectiveTimeout(),
		MaxBytes: policy.EffectiveMaxBytes(),
	})
	if err != nil {
		return err
	}

	mirrorUID := webMirrorUID(rawURL)
	title := strings.TrimSpace(result.Title)
	if title == "" {
		title = rawURL
	}

	content := webMirrorContent(mirrorUID, title, rawURL, result.Markdown)
	if _, err := s.IngestDocument(ctx, content); err != nil {
		return fmt.Errorf("ingest the mirrored page: %w", err)
	}

	hash := webMirrorHash(result.Markdown)
	state.record(rawURL, &WebMirror{
		SourceURL:   rawURL,
		UID:         mirrorUID,
		DeclaredBy:  []string{declaredBy},
		FetchedAt:   time.Now().UTC(),
		ContentHash: hash,
		Title:       title,
	})
	log.Printf("service: mirrored %q as %q (%d bytes of markdown)", rawURL, mirrorUID, len(result.Markdown))
	return nil
}

// webMirrorContent renders the mirror as a docbuilder document: the
// frontmatter carries the source URL (so chat citations link back
// to the original page) and the body is the converted markdown.
func webMirrorContent(uid, title, sourceURL, markdown string) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("uid: " + yamlQuote(uid) + "\n")
	b.WriteString("title: " + yamlQuote(title) + "\n")
	b.WriteString("fingerprint: auto-generated-if-empty\n")
	b.WriteString("source_kind: " + string(models.SourceWeb) + "\n")
	b.WriteString("urls:\n")
	b.WriteString("  - " + yamlQuote(sourceURL) + "\n")
	b.WriteString("metadata:\n")
	b.WriteString("  source_url: " + yamlQuote(sourceURL) + "\n")
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimSpace(markdown))
	b.WriteString("\n")
	return b.String()
}

// yamlQuote renders a string as a double-quoted YAML scalar.
func yamlQuote(s string) string {
	return strconv.Quote(s)
}

// webMirrorUID derives the mirror document's UID from the source
// URL, so the same page always maps to the same document.
//
// The slug keeps the host and the path readable ("web:example.com-
// spec-v2.0.0") while staying a single path segment.
func webMirrorUID(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return webMirrorUIDPrefix + webMirrorSlug(rawURL)
	}
	slug := webMirrorSlug(parsed.Host + parsed.Path)
	if parsed.RawQuery != "" {
		slug += "-" + webMirrorSlug(parsed.RawQuery)
	}
	return webMirrorUIDPrefix + slug
}

// webMirrorSlug reduces a string to lowercase alphanumerics,
// dots, and dashes — readable enough to recognize in a document
// list ("web:example.com-spec").
func webMirrorSlug(s string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// webMirrorHash hashes the converted markdown, so an unchanged page
// can be recognized without re-embedding it.
func webMirrorHash(markdown string) string {
	sum := sha256.Sum256([]byte(markdown))
	return hex.EncodeToString(sum[:])
}

// webMirrorState returns the service's mirror registry, loading it
// from disk on first use.
func (s *Service) webMirrorState() *webMirrorState {
	s.webMirrorsOnce.Do(func() {
		dir := s.config.VectorDB.PersistenceDir
		s.webMirrors = &webMirrorState{
			registry: &webMirrorRegistry{Mirrors: map[string]*WebMirror{}},
			path:     filepath.Join(dir, webMirrorStateFile),
		}
		if err := s.webMirrors.load(); err != nil {
			log.Printf("service: cannot read the web mirror registry at %s: %v", s.webMirrors.path, err)
		}
	})
	return s.webMirrors
}

// record stores a mirror in the registry.
func (m *webMirrorState) record(sourceURL string, mirror *WebMirror) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.registry.Mirrors == nil {
		m.registry.Mirrors = map[string]*WebMirror{}
	}
	existing := m.registry.Mirrors[sourceURL]
	if existing != nil {
		// Keep the declarers we already knew about.
		merged := map[string]struct{}{}
		for _, d := range existing.DeclaredBy {
			merged[d] = struct{}{}
		}
		for _, d := range mirror.DeclaredBy {
			merged[d] = struct{}{}
		}
		declarers := make([]string, 0, len(merged))
		for d := range merged {
			declarers = append(declarers, d)
		}
		sort.Strings(declarers)
		mirror.DeclaredBy = declarers
	}
	m.registry.Mirrors[sourceURL] = mirror
	m.saveLocked()
}

// setError records a failure for a source URL without touching the
// rest of the registry.
func (m *webMirrorState) setError(sourceURL, message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.registry.Mirrors == nil {
		m.registry.Mirrors = map[string]*WebMirror{}
	}
	existing := m.registry.Mirrors[sourceURL]
	if existing == nil {
		existing = &WebMirror{SourceURL: sourceURL}
		m.registry.Mirrors[sourceURL] = existing
	}
	existing.Error = message
	m.saveLocked()
}

// count returns how many mirrors the registry holds.
func (m *webMirrorState) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.registry.Mirrors)
}

// dropDeclarations removes the declarer from the sources it no
// longer requests, and returns the mirrors that lost their last
// declarer (those mirrors are garbage).
func (m *webMirrorState) dropDeclarations(declarer string, stillRequested map[string]struct{}) []WebMirror {
	m.mu.Lock()
	defer m.mu.Unlock()

	var orphans []WebMirror
	for sourceURL, mirror := range m.registry.Mirrors {
		if _, still := stillRequested[sourceURL]; still {
			continue
		}
		remaining := make([]string, 0, len(mirror.DeclaredBy))
		for _, d := range mirror.DeclaredBy {
			if d != declarer {
				remaining = append(remaining, d)
			}
		}
		if len(remaining) == 0 {
			orphans = append(orphans, *mirror)
			delete(m.registry.Mirrors, sourceURL)
			continue
		}
		mirror.DeclaredBy = remaining
	}
	if len(orphans) > 0 {
		sort.Slice(orphans, func(i, j int) bool { return orphans[i].SourceURL < orphans[j].SourceURL })
		m.saveLocked()
	}
	return orphans
}

// mirrors returns the registry's mirrors, sorted by source URL.
func (m *webMirrorState) mirrors() []WebMirror {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]WebMirror, 0, len(m.registry.Mirrors))
	for _, mirror := range m.registry.Mirrors {
		out = append(out, *mirror)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SourceURL < out[j].SourceURL })
	return out
}

// remove drops a source URL from the registry.
func (m *webMirrorState) remove(sourceURL string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.registry.Mirrors, sourceURL)
	m.saveLocked()
}

// load reads the registry from disk. A missing file is not an
// error: there are simply no mirrors yet.
func (m *webMirrorState) load() error {
	data, err := os.ReadFile(m.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var registry webMirrorRegistry
	if err := json.Unmarshal(data, &registry); err != nil {
		return err
	}
	if registry.Mirrors == nil {
		registry.Mirrors = map[string]*WebMirror{}
	}
	m.registry = &registry
	return nil
}

// saveLocked writes the registry to disk. The caller holds the
// lock.
func (m *webMirrorState) saveLocked() {
	data, err := json.MarshalIndent(m.registry, "", "  ")
	if err != nil {
		log.Printf("service: cannot encode the web mirror registry: %v", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		log.Printf("service: cannot create the web mirror registry directory: %v", err)
		return
	}
	if err := os.WriteFile(m.path, data, 0o644); err != nil {
		log.Printf("service: cannot write the web mirror registry at %s: %v", m.path, err)
	}
}
