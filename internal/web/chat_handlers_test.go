package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/require"
)

// TestChatTopK pins the chat-form top_k parser. The HTML
// form's max="50" attribute is advisory only; this
// server-side clamp is the security boundary (H-5) and the
// reason a curl request with top_k=1000000 cannot pull the
// entire vector DB into the LLM context. Anchoring the
// behavior in a direct test means a future contributor who
// adjusts the clamp sees the contract change explicitly.
func TestChatTopK(t *testing.T) {
	cases := []struct {
		name string
		form string
		want int
	}{
		{name: "missing form value uses default", form: "", want: defaultChatTopK},
		{name: "whitespace only uses default", form: "   ", want: defaultChatTopK},
		{name: "valid value passes through", form: "12", want: 12},
		{name: "zero uses default (not 0)", form: "0", want: defaultChatTopK},
		{name: "negative uses default", form: "-5", want: defaultChatTopK},
		{name: "non-numeric uses default", form: "abc", want: defaultChatTopK},
		{name: "above max clamps to max", form: "1000000", want: maxChatTopK},
		{name: "exactly at max passes through", form: "50", want: 50},
		{name: "one over max clamps", form: "51", want: maxChatTopK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(t.Context(),
				http.MethodPost, "/chat/message", nil)
			if tc.form != "" {
				r.Form = url.Values{"top_k": {tc.form}}
			}
			require.Equal(t, tc.want, chatTopK(r))
		})
	}
}

// TestParseSourceKindsForm pins the chat-form source_kinds
// parser. The chat form submits a comma-separated list of source
// kinds (e.g. "gitlab,docbuilder") and the handler must:
//
//   - return nil for empty / whitespace-only input (no filter)
//   - split on comma, trim whitespace
//   - drop empty entries (",," -> nil)
//   - drop entries that are not a known SourceKind (so a
//     forged form value can't smuggle a future kind past the
//     dispatch layer)
//   - be case-sensitive (the wire values are lowercase; "GitLab"
//     is rejected so the contract stays simple)
//
// The output feeds into service.SearchFilters.SourceKinds, which
// the vector layer applies as a post-filter. An empty result is
// the "no filter / all sources" interpretation, which matches
// what the chat form would send if the user cleared the
// multi-select.
func TestParseSourceKindsForm(t *testing.T) {
	cases := []struct {
		name string
		form string
		want []models.SourceKind
	}{
		{name: "empty -> nil (no filter)", form: "", want: nil},
		{name: "whitespace only -> nil", form: "   ", want: nil},
		{name: "single gitlab", form: "gitlab", want: []models.SourceKind{models.SourceGitLab}},
		{name: "single docbuilder", form: "docbuilder", want: []models.SourceKind{models.SourceDocbuilder}},
		{name: "two kinds comma-separated", form: "gitlab,docbuilder", want: []models.SourceKind{models.SourceGitLab, models.SourceDocbuilder}},
		{name: "whitespace tolerated around commas", form: " gitlab , docbuilder ", want: []models.SourceKind{models.SourceGitLab, models.SourceDocbuilder}},
		{name: "empty entries dropped", form: ",,", want: nil},
		{name: "empty entries between valid dropped", form: "gitlab,,docbuilder", want: []models.SourceKind{models.SourceGitLab, models.SourceDocbuilder}},
		{name: "unknown kind dropped, valid kept", form: "gitlab,redmine", want: []models.SourceKind{models.SourceGitLab}},
		{name: "all invalid -> nil", form: "redmine,confluence", want: nil},
		{name: "case-sensitive: 'GitLab' rejected", form: "GitLab", want: nil},
		{name: "trailing comma tolerated", form: "gitlab,", want: []models.SourceKind{models.SourceGitLab}},
		{name: "trailing whitespace tolerated", form: "gitlab   ", want: []models.SourceKind{models.SourceGitLab}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(t.Context(),
				http.MethodPost, "/chat/message", nil)
			if tc.form != "" {
				r.Form = url.Values{"source_kinds": {tc.form}}
			}
			got := parseSourceKindsForm(r)
			require.Equal(t, tc.want, got)
		})
	}
}
