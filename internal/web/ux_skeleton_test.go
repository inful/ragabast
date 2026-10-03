package web

import (
	"testing"

	"github.com/ragabast/internal/config"
	"github.com/stretchr/testify/require"
)

// TestUx_SkeletonClass pins the Phase 1.3 contract from
// plans/ux-overhaul.md: the daisyUI skeleton class is
// established as the canonical loading-state placeholder.
//
// Phase 1 introduces the class usage so the CSS is
// exercised and grep-able; Phase 4 wires it to actual
// loading flows (document fetch, search in-flight,
// chat-log hydration). The contract at this stage is:
// at least one element on a rendered full page carries
// class="skeleton".
//
// We pin the chat landing page because it is the
// app's main surface and the one most operators see
// first. A new install loads / and a long, blank log
// is the natural place to teach "more content is on
// its way" — a 2-line skeleton under the orienting
// copy says "your chat log will fill with content"
// without actually showing data that isn't there.
//
// The grep is for the class token (not the element
// type) so a future contributor can swap <div> for
// <span> or add a sizing utility without breaking
// the contract.
func TestUx_SkeletonClass(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg, &fakeHumaService{})
	s.templates = nil

	body := renderChatFallbackBody(t, s)

	// The base class "skeleton" must appear at least
	// once. The daisyUI class is the contract; the
	// element type, sizing utility, and container
	// layout are all designer choices that the test
	// intentionally does not pin.
	require.Contains(t, body, "skeleton",
		"the chat landing page must render at least one element with class=\"skeleton\" so the loading-state placeholder pattern is established")
}
