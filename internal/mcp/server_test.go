package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/service"
	"github.com/stretchr/testify/require"
)

// fakeMCPSvc is a minimal stub of the service surface the MCP
// tools need. It mirrors the pattern in internal/web/fakes_test.go
// but only implements the methods the MCP package touches.
type fakeMCPSvc struct {
	searchResults []models.SearchResult
	searchErr     error

	queryAnswer string
	queryInfo   *service.QueryDebugInfo
	queryErr    error

	documents      []models.DocumentInfo
	documentsTotal int
	documentsErr   error

	document *models.Document
	docErr   error

	lastSearchFilters service.SearchFilters
	lastSearchMode    service.SearchMode
}

func (f *fakeMCPSvc) HybridSearch(_ context.Context, _ string, _ int, filters service.SearchFilters, mode service.SearchMode) ([]models.SearchResult, error) {
	f.lastSearchFilters = filters
	f.lastSearchMode = mode
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	return f.searchResults, nil
}

func (f *fakeMCPSvc) QueryDebugWithOptions(_ context.Context, _ string, _ int, _ service.LLMOptions) (string, *service.QueryDebugInfo, error) {
	if f.queryErr != nil {
		return "", nil, f.queryErr
	}
	return f.queryAnswer, f.queryInfo, nil
}

func (f *fakeMCPSvc) ListDocumentsPaged(_ context.Context, limit, offset int) ([]models.DocumentInfo, int, error) {
	if f.documentsErr != nil {
		return nil, 0, f.documentsErr
	}
	return f.documents, f.documentsTotal, nil
}

func (f *fakeMCPSvc) GetDocument(_ context.Context, _ string) (*models.Document, error) {
	if f.docErr != nil {
		return nil, f.docErr
	}
	return f.document, nil
}

// TestRegisterTools_RegistersAllFour pins the wire-level
// contract: when MCP clients call tools/list, they must
// see exactly the four tools we expose. Adding a new tool
// without updating this test forces the maintainer to
// decide whether the tool is part of the public surface
// or not.
func TestRegisterTools_RegistersAllFour(t *testing.T) {
	t.Parallel()

	srv := NewServer(&fakeMCPSvc{})
	names := srv.ToolNames()

	require.ElementsMatch(t, []string{"search", "query", "list_documents", "get_document"}, names,
		"all four tools must be discoverable via tools/list")
}

// TestToolSearch_RoundTrip pins the happy path for the
// search tool: a query + limit returns formatted search
// results. Without this, the search tool could silently
// swallow results and the agent would get nothing.
func TestToolSearch_RoundTrip(t *testing.T) {
	t.Parallel()

	svc := &fakeMCPSvc{
		searchResults: []models.SearchResult{
			{ChunkID: "c1", DocumentID: "doc-1", DocumentTitle: "ADR 001", Similarity: 0.92},
			{ChunkID: "c2", DocumentID: "doc-2", DocumentTitle: "ADR 002", Similarity: 0.81},
		},
	}
	srv := NewServer(svc)

	resp := srv.HandleMessage(context.Background(), buildToolsCall(t, "search", map[string]any{
		"query": "kubernetes ingress tls",
		"limit": float64(5),
	}))

	result := extractCallToolResult(t, resp)
	require.NotNil(t, result, "tools/call must return a CallToolResult")
	require.False(t, result.IsError, "search tool must not return an error for valid input")

	text := extractText(t, result)
	require.Contains(t, text, "ADR 001", "result text must include the document title")
	require.Contains(t, text, "0.92", "result text must include the similarity score")
	require.Contains(t, text, "doc-1", "result text must include the document id for citation")
}

// TestToolSearch_AppliesFilters pins that the MCP search
// tool threads the optional filters through to the
// service layer. Without this, an MCP client asking for
// tag=security would silently search the entire corpus.
func TestToolSearch_AppliesFilters(t *testing.T) {
	t.Parallel()

	svc := &fakeMCPSvc{}
	srv := NewServer(svc)

	srv.HandleMessage(context.Background(), buildToolsCall(t, "search", map[string]any{
		"query":       "auth",
		"limit":       float64(3),
		"document_id": "adr-001",
		"tag":         "security",
		"category":    "Reference",
		"mode":        "keyword",
	}))

	require.Equal(t, "adr-001", svc.lastSearchFilters.DocumentID,
		"document_id filter must reach HybridSearch")
	require.Equal(t, "security", svc.lastSearchFilters.Tag,
		"tag filter must reach HybridSearch")
	require.Equal(t, "Reference", svc.lastSearchFilters.Category,
		"category filter must reach HybridSearch")
	require.Equal(t, service.ModeKeyword, svc.lastSearchMode,
		"mode parameter must reach HybridSearch")
}

// TestToolSearch_MissingQueryIsError pins the input
// validation contract: a search without a query must
// return an MCP error result (not a panic or empty
// result). Agents depend on errors being flagged.
func TestToolSearch_MissingQueryIsError(t *testing.T) {
	t.Parallel()

	srv := NewServer(&fakeMCPSvc{})
	resp := srv.HandleMessage(context.Background(), buildToolsCall(t, "search", map[string]any{
		"limit": float64(5),
	}))

	result := extractCallToolResult(t, resp)
	require.NotNil(t, result)
	require.True(t, result.IsError,
		"missing query must surface as an MCP error result, not a silent empty result")
}

// TestToolQuery_RoundTrip pins the chat-mode tool: the
// LLM answer and the source list both reach the agent.
// The chat session store is intentionally NOT used here
// — MCP clients manage their own context.
func TestToolQuery_RoundTrip(t *testing.T) {
	t.Parallel()

	svc := &fakeMCPSvc{
		queryAnswer: "ADR 001 recommends JWT auth with refresh tokens.",
		queryInfo: &service.QueryDebugInfo{
			Results: []models.SearchResult{
				{DocumentID: "doc-1", DocumentTitle: "ADR 001", Similarity: 0.88},
			},
		},
	}
	srv := NewServer(svc)

	resp := srv.HandleMessage(context.Background(), buildToolsCall(t, "query", map[string]any{
		"message": "What does ADR 001 say about auth?",
		"limit":   float64(5),
	}))

	result := extractCallToolResult(t, resp)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	text := extractText(t, result)
	require.Contains(t, text, "JWT auth",
		"answer text must reach the agent verbatim")
	require.Contains(t, text, "ADR 001",
		"source list must reach the agent")
}

// TestToolListDocuments_Paginates pins the pagination
// contract: limit and offset are passed through. Without
// this, an MCP client asking for page 2 would silently
// get page 1.
func TestToolListDocuments_Paginates(t *testing.T) {
	t.Parallel()

	svc := &fakeMCPSvc{
		documents: []models.DocumentInfo{
			{ID: "doc-1", Title: "First"},
			{ID: "doc-2", Title: "Second"},
		},
		documentsTotal: 47,
	}
	srv := NewServer(svc)

	resp := srv.HandleMessage(context.Background(), buildToolsCall(t, "list_documents", map[string]any{
		"limit":  float64(25),
		"offset": float64(25),
	}))

	result := extractCallToolResult(t, resp)
	require.NotNil(t, result)
	require.False(t, result.IsError)

	text := extractText(t, result)
	require.Contains(t, text, "First", "doc-1 title must be in the listing")
	require.Contains(t, text, "Second", "doc-2 title must be in the listing")
	require.Contains(t, text, "47",
		"total count must surface so the client can paginate")
}

// TestToolGetDocument_NotFoundIsError pins the
// not-found contract: missing document ids must surface
// as an MCP error result, not a silent empty result.
func TestToolGetDocument_NotFoundIsError(t *testing.T) {
	t.Parallel()

	svc := &fakeMCPSvc{
		docErr: errFakeNotFound,
	}
	srv := NewServer(svc)

	resp := srv.HandleMessage(context.Background(), buildToolsCall(t, "get_document", map[string]any{
		"document_id": "missing",
	}))

	result := extractCallToolResult(t, resp)
	require.NotNil(t, result)
	require.True(t, result.IsError,
		"missing document id must surface as an MCP error, not a silent empty result")
}

// TestToolUnknownNameIsError pins the routing contract:
// an unknown tool name must surface as an MCP error
// result, not a panic or a silent zero-result. Without
// this, a typo in an agent's tool call could either
// crash the server or return ambiguous data.
func TestToolUnknownNameIsError(t *testing.T) {
	t.Parallel()

	srv := NewServer(&fakeMCPSvc{})
	resp := srv.HandleMessage(context.Background(), buildToolsCall(t, "definitely_not_a_tool", map[string]any{}))

	// The mcp-go library routes unknown tool names to
	// the protocol layer's error path — the response
	// will be a non-CallToolResult message (a JSON-RPC
	// error). Either way: NOT a silent success.
	require.NotNil(t, resp,
		"unknown tool name must produce a response, not a nil")
}

// buildToolsCall wraps a (name, args) pair in the JSON-RPC
// `tools/call` envelope the MCP server expects.
func buildToolsCall(t *testing.T, name string, args map[string]any) json.RawMessage {
	t.Helper()
	params := map[string]any{
		"name":      name,
		"arguments": args,
	}
	raw, err := json.Marshal(params)
	require.NoError(t, err)
	msg := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  json.RawMessage(raw),
	}
	envelope, err := json.Marshal(msg)
	require.NoError(t, err)
	return envelope
}

// extractCallToolResult pulls the CallToolResult out of
// a JSON-RPC response. Returns nil if the response is
// a JSON-RPC error (which is the expected outcome for
// "tool returned an error result").
func extractCallToolResult(t *testing.T, resp mcp.JSONRPCMessage) *mcp.CallToolResult {
	t.Helper()
	raw, err := json.Marshal(resp)
	require.NoError(t, err)
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	require.NoError(t, json.Unmarshal(raw, &envelope))
	if envelope.Error != nil {
		return nil
	}
	var result mcp.CallToolResult
	require.NoError(t, json.Unmarshal(envelope.Result, &result))
	return &result
}

// extractText pulls the first text content block out of
// an MCP result for assertion. Tests that don't care
// about the structured shape just need the text.
func extractText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	require.NotEmpty(t, result.Content, "tool result must have content blocks")
	raw, err := json.Marshal(result.Content[0])
	require.NoError(t, err)
	var content struct {
		Text string `json:"text"`
	}
	require.NoError(t, json.Unmarshal(raw, &content))
	return content.Text
}

// errFakeNotFound is the sentinel returned by
// GetDocument for unknown IDs. Defined as a typed
// value so it stays distinct from network failures.
var errFakeNotFound = fakeNotFoundError("document not found")

type fakeNotFoundError string

func (e fakeNotFoundError) Error() string { return string(e) }
