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

// TestHandleChatMessage_ThreadsSourceKindsFilterToService pins
// the end-to-end data flow: a POST to /chat/message with a
// `source_kinds` form value must produce an LLMOptions.Filters
// call to the service with the parsed SourceKinds. The fake
// service captures the call so the test can assert on it. Without
// this test, the form parsing in parseSourceKindsForm and the
// QueryDebugWithOptions wiring in commit 4 would each be
// individually green but the chat handler could still silently
// drop the filter between them.
func TestHandleChatMessage_ThreadsSourceKindsFilterToService(t *testing.T) {
	cases := []struct {
		name            string
		form            string
		wantSourceKinds []models.SourceKind
	}{
		{
			name:            "no source_kinds form value -> empty filter (all sources)",
			form:            "message=hello",
			wantSourceKinds: nil,
		},
		{
			name:            "single gitlab -> [SourceGitLab]",
			form:            "message=hello&source_kinds=gitlab",
			wantSourceKinds: []models.SourceKind{models.SourceGitLab},
		},
		{
			name:            "two kinds -> both",
			form:            "message=hello&source_kinds=gitlab,docbuilder",
			wantSourceKinds: []models.SourceKind{models.SourceGitLab, models.SourceDocbuilder},
		},
		{
			name:            "unknown kind in form -> dropped",
			form:            "message=hello&source_kinds=gitlab,redmine",
			wantSourceKinds: []models.SourceKind{models.SourceGitLab},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.Paths.TemplatesDir = ""
			fake := &fakeService{
				queryDebug: &service.QueryDebugInfo{Results: []models.SearchResult{}},
			}
			s := NewServer(cfg, fake)

			req := httptest.NewRequestWithContext(t.Context(),
				http.MethodPost, "/chat/message",
				strings.NewReader(tc.form))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			s.router.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code,
				"chat handler must return 200 for a valid message with source_kinds filter")
			require.Equal(t, tc.wantSourceKinds, fake.lastQueryOpts.Filters.SourceKinds,
				"the LLMOptions.Filters.SourceKinds passed to QueryDebugWithOptions must match what the form parsed")
		})
	}
}

// TestHandleChatMessage_CitationLandsOnGitLabURL is the
// end-to-end version of the multi-source spec's headline fix:
// a chat message that cites a SourceGitLab source with both
// DocbuilderURL and DocumentURLs set must render the GitLab URL
// in the response, NOT the docbuilder permalink. This test
// exercises the full pipeline:
//
//	form (source_kinds=gitlab)
//	-> parseSourceKindsForm
//	-> LLMOptions.Filters
//	-> service.QueryDebugWithOptions (faked)
//	-> InlineSourceLinks + per-kind dispatch
//	-> markdown renderer
//	-> chat_message.html template
//
// All four commits (citation dispatch, source filter, gitlab
// ingest, docbuilder ingest) need to work together for this
// assertion to pass.
func TestHandleChatMessage_CitationLandsOnGitlabURL(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Paths.TemplatesDir = ""

	const gitlabURL = "https://gitlab.example.com/group/bar/-/issues/42"
	const docbuilderPermalink = "https://docs.example.com/_uid/gitlab:group/bar:42/"

	fake := &fakeService{
		queryDebug: &service.QueryDebugInfo{
			Results: []models.SearchResult{
				{
					DocumentTitle: "Issue 42",
					DocumentID:    "doc-1",
					UID:           "gitlab:group/bar:42",
					SourceKind:    models.SourceGitLab,
					DocumentURLs:  []string{gitlabURL},
					DocbuilderURL: docbuilderPermalink,
				},
			},
		},
		queryAnswer: "See [src:0].",
	}
	s := NewServer(cfg, fake)

	req := httptest.NewRequestWithContext(t.Context(),
		http.MethodPost, "/chat/message",
		strings.NewReader("message=hello&source_kinds=gitlab"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	body := w.Body.String()
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, body, gitlabURL,
		"the rendered response must cite the original GitLab URL for a SourceGitLab source")
	require.NotContains(t, body, docbuilderPermalink,
		"the rendered response must NOT cite the docbuilder permalink for a SourceGitLab source")
}

// TestHandleChatMessage_CitationLandsOnDocbuilderURL pins the
// inverse: a SourceDocbuilder source with both DocbuilderURL and
// a (legacy) DocumentURL set must still resolve to the
// DocbuilderURL. This is the existing docbuilder UX — preserved
// by commit 3's per-kind switch. Without this test, a future
// contributor "fixing" the docbuilder branch to prefer
// DocumentURLs (matching the gitlab branch) would regress.
func TestHandleChatMessage_CitationLandsOnDocbuilderURL(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Paths.TemplatesDir = ""

	const docbuilderPermalink = "https://docs.example.com/_uid/adr-001/"
	const legacyDocURL = "https://legacy.example.com/adr-001"

	fake := &fakeService{
		queryDebug: &service.QueryDebugInfo{
			Results: []models.SearchResult{
				{
					DocumentTitle: "ADR 001",
					DocumentID:    "doc-1",
					UID:           "adr-001",
					SourceKind:    models.SourceDocbuilder,
					DocumentURLs:  []string{legacyDocURL},
					DocbuilderURL: docbuilderPermalink,
				},
			},
		},
		queryAnswer: "See [src:0].",
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
	require.Contains(t, body, docbuilderPermalink,
		"a SourceDocbuilder source must cite the DocbuilderURL, not the legacy DocumentURL")
	require.NotContains(t, body, legacyDocURL,
		"the legacy DocumentURL must not surface in the citation for a SourceDocbuilder source")
}
