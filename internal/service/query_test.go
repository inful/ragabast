package service

import (
	"bytes"
	"context"
	"log"
	"testing"

	"github.com/ragabast/internal/config"
	"github.com/stretchr/testify/require"
)

// TestService_QueryDebugWithOptions_NoDeprecationLog pins the
// fix for issue #89: the deprecation warning Service.Query
// and Service.QueryDebugWithOptions used to emit has been
// removed because it pointed at Service.FindDocuments, a method
// that does not yet exist. Chat handlers still call
// QueryDebugWithOptions on every POST /chat/message; a
// misleading warning polluted startup logs and made the
// pending migration look cosmetic.
//
// The warning will return — under a build tag, gated on the
// race detector — once Service.FindDocuments lands. Until then,
// this test pins that the chat path emits no deprecation noise.
//
// We redirect log output to a buffer so the test doesn't pollute
// test stdout and can assert on the absence of the message.
func TestService_QueryDebugWithOptions_NoDeprecationLog(t *testing.T) {
	buf := &bytes.Buffer{}
	oldOut := log.Writer()
	oldFlags := log.Flags()
	log.SetOutput(buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(oldOut)
		log.SetFlags(oldFlags)
	})

	svc := &Service{config: config.DefaultConfig()}

	// The unbuilt LLM client panics on real work; recover so the
	// test asserts only on what we care about (the absence of the
	// deprecation log line).
	defer func() {
		_ = recover()
	}()

	_, _, _ = svc.QueryDebugWithOptions(context.Background(), "q", 5, LLMOptions{}) //nolint:dogsled // 3 returns expected before panic recovery

	out := buf.String()
	require.NotContains(t, out, "DEPRECATED",
		"deprecation warning must not fire from the chat path (issue #89); got: %q", out)
}
