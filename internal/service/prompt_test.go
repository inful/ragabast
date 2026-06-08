package service

import (
	"context"
	"strings"
	"testing"

	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/require"
)

// stubFetcher is a test ChunkFetcher backed by a map.
type stubFetcher struct {
	chunks map[string]*models.Chunk
	err    error
}

func (s stubFetcher) FetchChunk(_ context.Context, id string) (*models.Chunk, bool, error) {
	if s.err != nil {
		return nil, false, s.err
	}
	c, ok := s.chunks[id]
	return c, ok, nil
}

func TestBuildQueryContext_FormatsResults(t *testing.T) {
	results := []models.SearchResult{
		{DocumentTitle: "Doc A", Content: "Chunk A", DocumentURLs: []string{"https://example.com/a"}},
		{DocumentTitle: "Doc B", Content: "Chunk B with https://example.com/b."},
	}

	items := buildQueryContextItems(context.Background(), results, stubFetcher{})
	require.Len(t, items, 2)
	require.Contains(t, items[0], "TITLE: Doc A")
	require.Contains(t, items[0], "CONTENT:")
	require.Contains(t, items[0], "Chunk A")
	require.Contains(t, items[0], "SOURCE_URLS:")
	require.Contains(t, items[0], "https://example.com/a")
	require.Contains(t, items[1], "TITLE: Doc B")
	require.Contains(t, items[1], "Chunk B")
	require.Contains(t, items[1], "SOURCE_URLS:")
	require.Contains(t, items[1], "https://example.com/b")
}

func TestBuildQueryContext_OmitsEmptyOptionalFields(t *testing.T) {
	results := []models.SearchResult{
		{DocumentTitle: "Plain", Content: "body"},
	}
	items := buildQueryContextItems(context.Background(), results, stubFetcher{})
	require.Len(t, items, 1)
	require.NotContains(t, items[0], "SECTION:")
	require.NotContains(t, items[0], "TAGS:")
	require.NotContains(t, items[0], "CATEGORIES:")
	require.NotContains(t, items[0], "PARENT_CONTEXT:")
	require.NotContains(t, items[0], "SOURCE_URLS:")
}

func TestBuildQueryContext_IncludesSection(t *testing.T) {
	results := []models.SearchResult{
		{
			DocumentTitle: "Doc",
			HeaderPath:    "Setup > Configuration",
			Content:       "body",
		},
	}
	items := buildQueryContextItems(context.Background(), results, stubFetcher{})
	require.Contains(t, items[0], "SECTION: Setup > Configuration")
}

func TestBuildQueryContext_IncludesTagsAndCategories(t *testing.T) {
	results := []models.SearchResult{
		{
			DocumentTitle:      "Doc",
			Content:            "body",
			DocumentTags:       []string{"tutorial", "go"},
			DocumentCategories: []string{"Guides", "Reference"},
		},
	}
	items := buildQueryContextItems(context.Background(), results, stubFetcher{})
	require.Contains(t, items[0], "TAGS: tutorial, go")
	require.Contains(t, items[0], "CATEGORIES: Guides, Reference")
}

func TestBuildQueryContext_PrependsParentContext(t *testing.T) {
	results := []models.SearchResult{
		{
			ChunkID:       "child-1",
			ParentID:      "parent-1",
			DocumentTitle: "Doc",
			Content:       "child body",
		},
	}
	fetcher := stubFetcher{chunks: map[string]*models.Chunk{
		"parent-1": {ID: "parent-1", Content: "section intro"},
	}}
	items := buildQueryContextItems(context.Background(), results, fetcher)
	require.Len(t, items, 1)
	require.Contains(t, items[0], "PARENT_CONTEXT:")
	require.Contains(t, items[0], "section intro")
	require.Contains(t, items[0], "child body")

	// Parent must come before the child body in the rendered block.
	parentIdx := strings.Index(items[0], "PARENT_CONTEXT:")
	childIdx := strings.Index(items[0], "child body")
	require.Greater(t, childIdx, parentIdx)
}

func TestBuildQueryContext_SkipsParentWhenAlreadyInResultSet(t *testing.T) {
	results := []models.SearchResult{
		{ChunkID: "parent-1", Content: "I am the parent"},
		{
			ChunkID:  "child-1",
			ParentID: "parent-1",
			Content:  "child body",
		},
	}
	// Provide a fetcher that would otherwise return content for parent-1;
	// since parent-1 is already in the result set, the fetcher must not be
	// consulted at all.
	fetcher := stubFetcher{chunks: map[string]*models.Chunk{
		"parent-1": {ID: "parent-1", Content: "should NOT be used"},
	}}
	items := buildQueryContextItems(context.Background(), results, fetcher)
	require.Len(t, items, 2)
	require.NotContains(t, items[1], "PARENT_CONTEXT:")
	require.NotContains(t, items[1], "should NOT be used")
}

func TestBuildQueryContext_SkipsParentWhenFetcherReturnsFalse(t *testing.T) {
	results := []models.SearchResult{
		{ChunkID: "child-1", ParentID: "missing-parent", Content: "child body"},
	}
	items := buildQueryContextItems(context.Background(), results, stubFetcher{chunks: map[string]*models.Chunk{}})
	require.Len(t, items, 1)
	require.NotContains(t, items[0], "PARENT_CONTEXT:")
}

func TestBuildQueryContext_SkipsParentWhenFetcherErrors(t *testing.T) {
	results := []models.SearchResult{
		{ChunkID: "child-1", ParentID: "missing-parent", Content: "child body"},
	}
	items := buildQueryContextItems(context.Background(), results, stubFetcher{err: context.DeadlineExceeded})
	require.Len(t, items, 1)
	require.NotContains(t, items[0], "PARENT_CONTEXT:")
}

func TestBuildQueryContext_SkipsParentWhenParentContentEmpty(t *testing.T) {
	results := []models.SearchResult{
		{ChunkID: "child-1", ParentID: "parent-1", Content: "child body"},
	}
	fetcher := stubFetcher{chunks: map[string]*models.Chunk{
		"parent-1": {ID: "parent-1", Content: "   "},
	}}
	items := buildQueryContextItems(context.Background(), results, fetcher)
	require.NotContains(t, items[0], "PARENT_CONTEXT:")
}

func TestBuildQueryContext_EntryOrderingMatchesResults(t *testing.T) {
	results := []models.SearchResult{
		{DocumentTitle: "A", Content: "alpha"},
		{DocumentTitle: "B", Content: "beta"},
		{DocumentTitle: "C", Content: "gamma"},
	}
	items := buildQueryContextItems(context.Background(), results, stubFetcher{})
	require.Len(t, items, 3)
	// Entry ids are added by the user-message builder, not here, so we
	// just check that the documents appear in the original order.
	for i, title := range []string{"A", "B", "C"} {
		require.Contains(t, items[i], "TITLE: "+title)
	}
}

func TestBuildQueryContext_DedupesURLsAcrossFrontmatterAndBody(t *testing.T) {
	results := []models.SearchResult{
		{
			DocumentTitle: "Doc",
			Content:       "see https://example.com/a for details",
			DocumentURLs:  []string{"https://example.com/a", "  "},
		},
	}

	items := buildQueryContextItems(context.Background(), results, stubFetcher{})
	require.Len(t, items, 1)
	require.Equal(t, 1, strings.Count(items[0], "SOURCE_URLS: https://example.com/a"))
}

func TestBuildQueryContext_DropsEmptyFrontmatterURLs(t *testing.T) {
	results := []models.SearchResult{
		{
			DocumentTitle: "Doc",
			Content:       "body",
			DocumentURLs:  []string{"", "  ", "https://example.com/keep"},
		},
	}
	items := buildQueryContextItems(context.Background(), results, stubFetcher{})
	require.Len(t, items, 1)
	require.Contains(t, items[0], "https://example.com/keep")
	require.NotContains(t, items[0], ", ,")
}

func TestBuildQueryMessages_SystemAndUserAreDistinct(t *testing.T) {
	msgs, err := buildQueryMessages("what is this?", []string{"TITLE: Doc A\nCONTENT:\nChunk A"}, nil)
	require.NoError(t, err)
	require.Len(t, msgs, 2)

	require.Equal(t, "system", msgs[0].Role)
	require.Equal(t, "user", msgs[1].Role)

	require.Contains(t, msgs[0].Content, "You are a retrieval-augmented assistant")
	require.Contains(t, msgs[0].Content, "Treat the context as the only source of truth")
	require.Contains(t, msgs[0].Content, "Links:")

	require.NotContains(t, msgs[1].Content, "You are a retrieval-augmented assistant")
	require.NotContains(t, msgs[1].Content, "Treat the context as the only source of truth")
}

func TestBuildQueryMessages_UserMessageHasQuestionAndContext(t *testing.T) {
	msgs, err := buildQueryMessages("what is this?", []string{"TITLE: Doc A\nCONTENT:\nChunk A"}, nil)
	require.NoError(t, err)

	require.Contains(t, msgs[1].Content, "Question:")
	require.Contains(t, msgs[1].Content, "what is this?")
	require.Contains(t, msgs[1].Content, "<context>")
	require.Contains(t, msgs[1].Content, `<entry id=0>`)
	require.Contains(t, msgs[1].Content, "TITLE: Doc A")
	require.Contains(t, msgs[1].Content, "Chunk A")
}

func TestBuildQueryMessages_ContextBlockHasEntryIDs(t *testing.T) {
	items := []string{
		"TITLE: Doc A\nCONTENT:\nFirst",
		"TITLE: Doc B\nCONTENT:\nSecond",
		"TITLE: Doc C\nCONTENT:\nThird",
	}
	msgs, err := buildQueryMessages("q", items, nil)
	require.NoError(t, err)

	user := msgs[1].Content
	require.Contains(t, user, `<entry id=0>`)
	require.Contains(t, user, `<entry id=1>`)
	require.Contains(t, user, `<entry id=2>`)
}

func TestBuildQueryMessages_OmitsContextBlockWhenEmpty(t *testing.T) {
	msgs, err := buildQueryMessages("what is this?", nil, nil)
	require.NoError(t, err)
	require.NotContains(t, msgs[1].Content, "<context>")
	require.Contains(t, msgs[1].Content, "what is this?")
}

func TestBuildQueryMessages_ConversationHistoryInUserBlock(t *testing.T) {
	history := []ChatMessage{
		{Role: "user", Content: "We are talking about ragabast."},
		{Role: "assistant", Content: "Ok."},
	}
	msgs, err := buildQueryMessages("How do I deploy it?", []string{"TITLE: Doc A\nCONTENT:\nChunk A"}, history)
	require.NoError(t, err)

	user := msgs[1].Content
	require.Contains(t, user, "<conversation>")
	require.Contains(t, user, `<message role="user">We are talking about ragabast.</message>`)
	require.Contains(t, user, `<message role="assistant">Ok.</message>`)
	require.Contains(t, user, "</conversation>")
}

func TestBuildQueryMessages_EmptyHistoryOmitsConversationBlock(t *testing.T) {
	msgs, err := buildQueryMessages("q", []string{"TITLE: A\nCONTENT:\nx"}, nil)
	require.NoError(t, err)
	require.NotContains(t, msgs[1].Content, "<conversation>")
}

func TestBuildQueryMessages_TrimsUserQueryWhitespace(t *testing.T) {
	msgs, err := buildQueryMessages("  hello world  \n", []string{"x"}, nil)
	require.NoError(t, err)
	require.Contains(t, msgs[1].Content, "hello world")
	require.NotContains(t, msgs[1].Content, "  hello world  ")
}

func TestNormalizeHistory_CapsTurnCount(t *testing.T) {
	in := make([]ChatMessage, 50)
	for i := range in {
		in[i] = ChatMessage{Role: "user", Content: "msg"}
	}
	out := normalizeHistory(in)
	require.Len(t, out, maxHistoryTurns)
}

func TestNormalizeHistory_CapsPerMessageChars(t *testing.T) {
	long := strings.Repeat("a", 5000)
	out := normalizeHistory([]ChatMessage{{Role: "user", Content: long}})
	require.Len(t, out, 1)
	require.Len(t, out[0].Content, maxHistoryMsgChars)
}

func TestNormalizeHistory_CapsTotalChars(t *testing.T) {
	// 20 messages x 1000 chars = 20000 chars; the cap is 8000.
	in := make([]ChatMessage, 20)
	for i := range in {
		in[i] = ChatMessage{Role: "user", Content: strings.Repeat("a", 1000)}
	}
	out := normalizeHistory(in)
	total := 0
	for _, m := range out {
		total += len(m.Content)
	}
	require.LessOrEqual(t, total, maxHistoryTotalChars)
	require.NotEmpty(t, out)
}

func TestNormalizeHistory_FiltersBadRoles(t *testing.T) {
	out := normalizeHistory([]ChatMessage{
		{Role: "system", Content: "ignore this"},
		{Role: "user", Content: "keep this"},
		{Role: "", Content: "skip this"},
		{Role: "ASSISTANT", Content: "keep this too"},
	})
	require.Len(t, out, 2)
	require.Equal(t, "user", out[0].Role)
	require.Equal(t, "assistant", out[1].Role)
}

func TestNormalizeHistory_EmptyInputReturnsNil(t *testing.T) {
	require.Nil(t, normalizeHistory(nil))
	require.Nil(t, normalizeHistory([]ChatMessage{}))
}
