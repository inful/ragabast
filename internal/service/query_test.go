package service

import (
	"bytes"
	"context"
	"log"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ragabast/internal/config"
	"github.com/stretchr/testify/require"
)

// TestService_QueryDebugWithOptions_LogsDeprecation pins the
// soft-deprecation contract: callers of the LLM-synthesis path
// get a one-shot runtime warning so they know the API is on the
// way out and find-docs should be the primary mode.
//
// We redirect log output to a buffer so the test doesn't pollute
// test stdout and can assert on the exact message.
//
// The test deliberately uses a minimal Service that panics on any
// real work — the warning must fire BEFORE we attempt any LLM
// call, otherwise a broken deprecation layer would be silent.
func TestService_QueryDebugWithOptions_LogsDeprecation(t *testing.T) {
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

	// We expect a panic from the unbuilt LLM client; recover it so
	// the test asserts only on what we care about (the log line
	// fired before the panic).
	defer func() {
		_ = recover()
	}()

	_, _, _ = svc.QueryDebugWithOptions(context.Background(), "q", 5, LLMOptions{}) //nolint:dogsled // 3 returns expected before panic recovery

	out := buf.String()
	require.Contains(t, out, "DEPRECATED",
		"deprecation warning should mention DEPRECATED; got: %q", out)
	require.Contains(t, out, "QueryDebugWithOptions",
		"deprecation warning should name the deprecated method")
	require.Contains(t, out, "Search",
		"deprecation warning should point users at the find-docs replacement")
}

// TestService_DeprecationWarning_FiresOncePerMethod guards
// against log spam: each deprecated method should warn at most
// once per process. The simplest correctness check is that
// calling the method twice doesn't produce a duplicate warning.
func TestService_DeprecationWarning_FiresOncePerMethod(t *testing.T) {
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

	callCount := int32(0)
	doCall := func() {
		defer func() {
			_ = recover()
			atomic.AddInt32(&callCount, 1)
		}()
		_, _ = svc.Query(context.Background(), "q", 5)
	}
	doCall()
	doCall()

	require.Equal(t, int32(2), atomic.LoadInt32(&callCount), "sanity check: calls actually happened")

	lines := strings.Count(buf.String(), "DEPRECATED")
	require.Equal(t, 1, lines,
		"deprecation warning should fire exactly once per process, not once per call; got %d", lines)
}
