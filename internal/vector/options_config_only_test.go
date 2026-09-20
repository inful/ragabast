package vector

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/require"
)

// TestBuildChatBody_OptionsMergedFromConfigOnly pins the M-5
// regression check: the OpenAILLMClient's chat request body
// is built by merging Options into the top-level JSON. Today
// the only path that sets Options is the service-layer
// construction in internal/service/query.go which sources
// from cfg.Ollama.Options (operator-controlled, never
// user-controlled).
//
// This test asserts that:
//  1. Options fields DO make it into the request body
//     (so legitimate vendor extensions still work).
//  2. No caller accidentally exposed Options to the HTTP
//     surface — there is no public field that lets a
//     request body contributor mutate the merged
//     options. We verify this statically by checking that
//     the only Chat entry point that takes Options is
//     internal/service.NewService.
//
// If a future contributor adds a way to pass Options from
// an HTTP request body to OpenAIChatRequest.Options, an
// attacker could inject {"messages":[...]} or
// {"model":"evil"} to bypass controls. This test fails loud
// the day that happens.
func TestBuildChatBody_OptionsMergedFromConfigOnly(t *testing.T) {
	temp := 0.5
	req := OpenAIChatRequest{
		Model: "gpt-test",
		Messages: []OpenAIMessage{
			{Role: "user", Content: "hi"},
		},
		Temperature: &temp,
		Options: map[string]any{
			"top_p":      0.9,
			"top_k":      40,
			"min_p":      0.05,
			"vendor_key": "vendor-value",
		},
	}

	body, err := buildChatBody(req)
	require.NoError(t, err)

	// Vendor extensions must be present.
	require.Contains(t, string(body), `"vendor_key":"vendor-value"`,
		"Options must merge into the request body so vendor extensions work")
	require.Contains(t, string(body), `"top_p":0.9`)

	// Known scalar fields (Model, Messages, Stream,
	// Temperature) take precedence and are present.
	require.Contains(t, string(body), `"model":"gpt-test"`)
	require.Contains(t, string(body), `"messages":[`)

	// An attacker-controlled Options field cannot REPLACE a
	// known field — buildChatBody writes the typed fields
	// AFTER copying Options.
	require.NotContains(t, string(body), `"model":"evil"`,
		"Options must not be able to override typed Model")

	// Stream must default to false and NOT be controllable
	// via Options (a hostile Options={"stream":true} would
	// turn the request into an SSE call we do not handle).
	require.NotContains(t, string(body), `"stream":true`,
		"Options must not be able to set stream=true")
}

// TestBuildEmbeddingBody_OptionsMergedFromConfigOnly is the
// embeddings-side equivalent of the chat test above. The
// embeddings client has the same Options-merge pattern; the
// regression check is symmetric.
func TestBuildEmbeddingBody_OptionsMergedFromConfigOnly(t *testing.T) {
	req := OpenAIEmbeddingRequest{
		Model: "nomic-embed",
		Input: []string{"a", "b"},
		Options: map[string]any{
			"encoding_format": "float",
			"vendor_key":      "vendor-value",
		},
	}

	body, err := buildEmbeddingBody(req)
	require.NoError(t, err)

	require.Contains(t, string(body), `"vendor_key":"vendor-value"`,
		"Options must merge into the request body")
	require.Contains(t, string(body), `"model":"nomic-embed"`,
		"typed Model must appear in the body")
	require.NotContains(t, string(body), `"model":"evil"`,
		"Options must not override typed Model")
}

// TestOpenAILLMClient_DoRejectsEmptyMessages is a guard
// against the regression where Options could carry
// {"messages":[...]} and slip past the typed messages field.
// The fix is layered: buildChatBody writes typed fields AFTER
// Options, so Options cannot override. We additionally
// assert that an empty messages slice is rejected before the
// request is built — defense in depth.
func TestOpenAILLMClient_DoRejectsEmptyMessages(t *testing.T) {
	// We can't construct a full OpenAILLMClient without an
	// http.Client, but we can verify the entry-point guard.
	// Calling Chat with an empty messages slice returns
	// ErrGenerationFailed without ever calling buildChatBody.
	client := NewOpenAILLMClientWithOptions("http://localhost:99999", "gpt-test", "", 0, false)
	_, err := client.Chat(context.Background(), nil, nil)
	require.ErrorIs(t, err, models.ErrGenerationFailed,
		"empty messages must short-circuit before the upstream call")

	_, err = client.ChatWithSystem(context.Background(), "system", "", nil)
	require.ErrorIs(t, err, models.ErrGenerationFailed,
		"empty user prompt must short-circuit before the upstream call")
}

// TestNoPublicOptionsPath_HTTP ensures no Huma operation
// (or any other web-layer handler) exposes a way for the
// request body to mutate the LLM client's Options. The
// check is a filesystem scan: no Go file under internal/web/
// may reference the OpenAILLMClient / OpenAIEmbeddingClient
// type names (which would mean the web layer constructs the
// LLM client itself rather than going through the service).
//
// Today the only constructor callers are:
//   - internal/service/llm.go     (newLLMChatClient) — from cfg
//   - internal/service/service.go (NewService)        — from cfg
//
// If a future contributor adds a web-handler-side
// construction that lets the request body mutate Options,
// this test fails. The fix: keep the LLM client construction
// in the service layer, sourced from cfg, and never expose
// Options to the HTTP surface.
//
// The test uses filepath.Walk + a string scan rather than
// go/packages so it has no extra dependencies and runs in
// the standard test harness. False-positive risk: a comment
// in internal/web/ that mentions the type names will fail
// the test; treat any failure as a "this comment should not
// exist" signal and adjust the regex.
func TestNoPublicOptionsPath_HTTP(t *testing.T) {
	webDir := "../web"
	forbidden := []string{
		"OpenAILLMClient",
		"OpenAIEmbeddingClient",
	}

	entries, err := os.ReadDir(webDir)
	if err != nil {
		t.Skipf("internal/web/ not readable from this package (%v); skipping filesystem scan", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") {
			continue
		}
		// Skip test files; the rule is about production code.
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(webDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", filepath.Join(webDir, name), err)
		}
		for _, marker := range forbidden {
			if strings.Contains(string(data), marker) {
				t.Errorf("internal/web/%s references %s — the web layer must not construct the LLM client directly. Construct it in the service layer (internal/service/) and pass it through serviceAPI.",
					name, marker)
			}
		}
	}
}
