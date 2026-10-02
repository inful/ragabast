package web

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSafeNextPath pins the "next=" parameter normalizer
// used by pageHeaderFromContext. The function is a
// one-liner today, but a test pins the contract: empty in
// must mean "/" out (so the chooser's ?next= is never
// empty) and any non-empty path is returned verbatim. A
// future contributor who adds path-allow-listing or
// percent-encoding will see this test fail and know the
// contract they're changing.
func TestSafeNextPath(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty becomes root", in: "", want: "/"},
		{name: "non-empty passes through", in: "/documents", want: "/documents"},
		{name: "query-bearing path passes through", in: "/chat?foo=bar", want: "/chat?foo=bar"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, safeNextPath(tc.in))
		})
	}
}
