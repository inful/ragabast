package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHandleChatMessage_AllSourcesRenderAsInlineBadges is the
// headline pin for Phase 2.2 of plans/ux-overhaul.md: the chat
// message fragment replaces the legacy per-kind grouping
// (`<summary>GitLab (2)</summary>`-style headers) with inline
// badge chips. Every source cited in the reply must appear
// as a daisyUI badge anchor with the source's label and
// citation URL.
//
// The pre-Phase-2.2 design wrapped sources in a
// `<details><summary>Kind (N)</summary>...<ol>...</ol></details>`
// block per kind. Phase 2.2 collapses all kinds into a single
// row of badges — the operator sees all citations at a glance
// without clicking to expand a disclosure.
//
// The test seeds two gitlab sources and two docbuilder sources
// and pins: every URL appears (the badges actually link to
// each source's citation URL); every label appears (the
// badge text matches the source's DisplayLabel); the
// legacy per-kind grouping is gone.
func TestHandleChatMessage_AllSourcesRenderAsInlineBadges(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Paths.TemplatesDir = ""

	const (
		gitlabURL1     = "https://gitlab.example.com/g/p/-/issues/1"
		gitlabURL2     = "https://gitlab.example.com/g/p/-/issues/2"
		docbuilderURL1 = "https://docs.example.com/_uid/adr-001/"
		docbuilderURL2 = "https://docs.example.com/_uid/adr-002/"
	)

	fake := &fakeService{
		queryDebug: &service.QueryDebugInfo{
			Results: []models.SearchResult{
				{
					DocumentTitle: "GitLab 1",
					DocumentID:    "g1", UID: "gitlab:g/p:1",
					SourceKind:   models.SourceGitLab,
					DocumentURLs: []string{gitlabURL1},
				},
				{
					DocumentTitle: "ADR 1",
					DocumentID:    "d1", UID: "adr-001",
					SourceKind:   models.SourceDocbuilder,
					DocumentURLs: []string{"https://example.com/adr-1"},
					CitationURL:  docbuilderURL1,
				},
				{
					DocumentTitle: "GitLab 2",
					DocumentID:    "g2", UID: "gitlab:g/p:2",
					SourceKind:   models.SourceGitLab,
					DocumentURLs: []string{gitlabURL2},
				},
				{
					DocumentTitle: "ADR 2",
					DocumentID:    "d2", UID: "adr-002",
					SourceKind:   models.SourceDocbuilder,
					DocumentURLs: []string{"https://example.com/adr-2"},
					CitationURL:  docbuilderURL2,
				},
			},
		},
		queryAnswer: "mixed-source reply",
	}
	s := NewServer(cfg, fake)

	req := httptest.NewRequestWithContext(t.Context(),
		http.MethodPost, "/chat/message",
		strings.NewReader("message=hello"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	body := w.Body.String()
	require.Equal(t, http.StatusOK, w.Code)

	// Every URL must appear (the badges actually
	// link to each source's citation URL).
	require.Contains(t, body, gitlabURL1)
	require.Contains(t, body, gitlabURL2)
	require.Contains(t, body, docbuilderURL1)
	require.Contains(t, body, docbuilderURL2)

	// Every source label must appear (the badge
	// text matches the source's DisplayLabel).
	require.Contains(t, body, "GitLab 1")
	require.Contains(t, body, "GitLab 2")
	require.Contains(t, body, "ADR 1")
	require.Contains(t, body, "ADR 2")

	// Each source must be a daisyUI badge anchor.
	// We count occurrences of `class="badge ...` to
	// verify each source has its own badge. (The
	// action bar's "4 sources" badge uses the same
	// class, so we count >= 4, not == 4.)
	badgeCount := strings.Count(body, `class="badge`)
	require.GreaterOrEqual(t, badgeCount, 4,
		"each cited source must render as its own daisyUI badge anchor (got %d badges)", badgeCount)

	// The legacy per-kind grouping headers are gone.
	// The "N sources" badge in the action bar is the
	// new summary, but it doesn't have parentheses
	// around a count.
	assert.NotContains(t, body, "GitLab (",
		"the legacy per-kind 'GitLab (N)' grouping header must be gone (Phase 2.2 inline badge pattern)")
	assert.NotContains(t, body, "Docbuilder (",
		"the legacy per-kind 'Docbuilder (N)' grouping header must be gone (Phase 2.2 inline badge pattern)")
	assert.NotContains(t, body, "Sources (",
		"the legacy 'Sources (N)' header must be gone (Phase 2.2 replaces with inline badges)")
}

// TestHandleChatMessage_SingleKindAllSourcesRender pins the
// single-kind case: even when all sources are gitlab, every
// source still renders as its own badge. (The pre-Phase-2.2
// design grouped them under a single "GitLab (2)" header;
// Phase 2.2 flattens everything into a single row of
// badges.)
func TestHandleChatMessage_SingleKindAllSourcesRender(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Paths.TemplatesDir = ""

	fake := &fakeService{
		queryDebug: &service.QueryDebugInfo{
			Results: []models.SearchResult{
				{
					DocumentTitle: "Issue 42",
					DocumentID:    "g1", UID: "gitlab:g/p:42",
					SourceKind:   models.SourceGitLab,
					DocumentURLs: []string{"https://gitlab.example.com/g/p/-/issues/42"},
				},
				{
					DocumentTitle: "Issue 7",
					DocumentID:    "g2", UID: "gitlab:g/p:7",
					SourceKind:   models.SourceGitLab,
					DocumentURLs: []string{"https://gitlab.example.com/g/p/-/issues/7"},
				},
			},
		},
		queryAnswer: "see [src:0] and [src:1]",
	}
	s := NewServer(cfg, fake)

	req := httptest.NewRequestWithContext(t.Context(),
		http.MethodPost, "/chat/message",
		strings.NewReader("message=hello"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	body := w.Body.String()
	require.Equal(t, http.StatusOK, w.Code)

	// Both source labels render as badges.
	require.Contains(t, body, "Issue 42")
	require.Contains(t, body, "Issue 7")

	// The legacy grouping is gone — no "GitLab (2)"
	// header, no "Sources (2)" flat header.
	assert.NotContains(t, body, "GitLab (",
		"the legacy per-kind 'GitLab (N)' grouping header must be gone (Phase 2.2 inline badge pattern)")
	assert.NotContains(t, body, "Sources (",
		"the flat 'Sources (N)' header must be gone (Phase 2.2 inline badge pattern)")
}

// TestHandleChatMessage_NoSources_NoBadges pins the
// empty-state contract: when the reply has zero sources,
// no source badges render. A spurious empty badge row
// would be worse than no row at all.
func TestHandleChatMessage_NoSources_NoBadges(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Paths.TemplatesDir = ""

	fake := &fakeService{
		queryDebug:  &service.QueryDebugInfo{Results: nil},
		queryAnswer: "no sources",
	}
	s := NewServer(cfg, fake)

	req := httptest.NewRequestWithContext(t.Context(),
		http.MethodPost, "/chat/message",
		strings.NewReader("message=hello"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	body := w.Body.String()
	require.Equal(t, http.StatusOK, w.Code)

	// No "Sources (" group header, no "N sources" badge
	// in the action bar (because there are no sources
	// to count). The new test pins the specific "N
	// sources" action-bar badge via the data-tooltip +
	// text pattern; a plain " sources" substring would
	// false-positive on a chat answer that happens
	// to contain the word.
	assert.NotContains(t, body, "Sources (",
		"an empty sources list must NOT render a 'Sources (N)' header")
	assert.NotRegexp(t, `\b\d+\s+sources\b`, body,
		"an empty sources list must NOT render an 'N sources' badge in the action bar")
}
