package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ragabast/internal/config"
	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/service"
	"github.com/stretchr/testify/require"
)

// TestHandleChatMessage_SourcesGroupedByKind is the headline
// pin for Stage 2.3: the chat sources panel must group retrieved
// chunks by source kind so an operator skimming a multi-source
// reply can see "2 from GitLab, 2 from docs" rather than a
// flat list where the kinds are indistinguishable.
//
// The test seeds two gitlab sources and two docbuilder sources
// and asserts on the GROUPED-OUTPUT per-kind headers (not just
// on URL presence, which would also pass against the flat
// template). The current flat template outputs a single
// `<summary>Sources (4)</summary>` and one <ol>; the grouped
// template outputs two `<summary>` blocks with kind names + per-
// kind counts. The assertions on the group-header text
// distinguish the two shapes.
func TestHandleChatMessage_SourcesGroupedByKind(t *testing.T) {
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

	// Each kind must appear as a group header with its count.
	// Pinning the count distinguishes the grouped layout
	// (two `<summary>GitLab (2)</summary>`-style blocks) from
	// the current flat layout (one `<summary>Sources (4)</summary>`).
	require.Contains(t, body, "GitLab (2)",
		"the chat sources panel must render a GitLab group with count 2; flat layout outputs only 'Sources (4)'")
	require.Contains(t, body, "Docbuilder (2)",
		"the chat sources panel must render a Docbuilder group with count 2; flat layout outputs only 'Sources (4)'")

	// All four URLs must appear.
	require.Contains(t, body, gitlabURL1)
	require.Contains(t, body, gitlabURL2)
	require.Contains(t, body, docbuilderURL1)
	require.Contains(t, body, docbuilderURL2)

	// The flat "Sources (4)" header must NOT appear — that's
	// the marker of the un-grouped layout this commit replaces.
	require.NotContains(t, body, "Sources (4)",
		"the flat 'Sources (4)' header must not appear after grouping")
}

// TestHandleChatMessage_SingleKindStillGroups pins the
// single-kind case: even when all sources are gitlab, the
// panel still groups (one group header is still useful —
// tells the operator what kind of sources the LLM drew
// from). Counts down from the headline test.
func TestHandleChatMessage_SingleKindStillGroups(t *testing.T) {
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
	require.Contains(t, body, "GitLab (2)",
		"a single-kind reply must still group — one group header is useful")
	require.NotContains(t, body, "Sources (2)",
		"flat 'Sources (2)' header must not appear after grouping")
}

// TestHandleChatMessage_NoSources_NoGroupHeader pins the
// empty-state contract: "Sources:" headers must not appear
// when the reply has zero sources. A spurious empty group
// would render as "Sources (0)" — worse than no panel at all.
func TestHandleChatMessage_NoSources_NoGroupHeader(t *testing.T) {
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
	require.NotContains(t, body, "Sources (",
		"an empty sources list must NOT render a 'Sources (0)' header")
}
