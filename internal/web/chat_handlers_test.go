package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

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
