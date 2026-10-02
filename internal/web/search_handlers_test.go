package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSearchTopK pins the search-form top_k parser. The
// server-side clamp at maxSearchTopK is the security
// boundary (H-5) — the HTML form's max="50" attribute is
// advisory only and can be bypassed by curl. The parser
// returns the default for empty / invalid / non-positive
// input and clamps oversized values to maxSearchTopK so a
// single request cannot exhaust the search index.
func TestSearchTopK(t *testing.T) {
	cases := []struct {
		name string
		form string
		want int
	}{
		{name: "missing form value uses default", form: "", want: defaultSearchTopK},
		{name: "whitespace only uses default", form: "   ", want: defaultSearchTopK},
		{name: "valid value passes through", form: "12", want: 12},
		{name: "zero uses default (not 0)", form: "0", want: defaultSearchTopK},
		{name: "negative uses default", form: "-5", want: defaultSearchTopK},
		{name: "non-numeric uses default", form: "abc", want: defaultSearchTopK},
		{name: "above max clamps to max", form: "1000000", want: maxSearchTopK},
		{name: "exactly at max passes through", form: "50", want: 50},
		{name: "one over max clamps", form: "51", want: maxSearchTopK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(t.Context(),
				http.MethodPost, "/search", nil)
			if tc.form != "" {
				r.Form = url.Values{"top_k": {tc.form}}
			}
			require.Equal(t, tc.want, searchTopK(r))
		})
	}
}
