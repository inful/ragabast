package vector

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestApplyDocPrompt_SubstitutesTitle pins the {title} placeholder:
// a doc prompt can carry the document's title, which is what the
// EmbeddingGemma model card asks for ("title: {title} | text:
// {content}"). Without the placeholder the title could not be
// included at all — the prompt is otherwise a static prefix.
func TestApplyDocPrompt_SubstitutesTitle(t *testing.T) {
	got := applyDocPrompt("title: {title} | text: ", "Getting Started", "body text")
	assert.Equal(t, "title: Getting Started | text: body text", got)
}

// TestApplyDocPrompt_TitleLessDocumentUsesNone pins the model
// card's convention: a document with no title is announced as
// "none", not as an empty string.
func TestApplyDocPrompt_TitleLessDocumentUsesNone(t *testing.T) {
	got := applyDocPrompt("title: {title} | text: ", "", "body text")
	assert.Equal(t, "title: none | text: body text", got)
}

// TestApplyDocPrompt_WithoutPlaceholderIsVerbatim pins that the
// models that take no task input keep working unchanged.
func TestApplyDocPrompt_WithoutPlaceholderIsVerbatim(t *testing.T) {
	got := applyDocPrompt("task: retrieval.passage: ", "Getting Started", "body text")
	assert.Equal(t, "task: retrieval.passage: body text", got)
}

// TestApplyDocPrompt_EmptyPromptIsIdentity pins the hot path:
// no prompt means no prepend and no allocation.
func TestApplyDocPrompt_EmptyPromptIsIdentity(t *testing.T) {
	got := applyDocPrompt("", "Getting Started", "body text")
	assert.Equal(t, "body text", got)
}

// TestGenerateChunkEmbeddings_UsesDocumentTitle pins the wiring:
// the chunk's title reaches the prompt. The chunk carries the
// document title (set by the vector layer before the embed call),
// so the model gets the title the model card asks for.
func TestGenerateChunkEmbeddings_UsesDocumentTitle(t *testing.T) {
	var got struct {
		Input []string `json:"input"`
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		assert.NoError(t, json.Unmarshal(body, &got))

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.1,0.2,0.3]},{"object":"embedding","index":1,"embedding":[0.4,0.5,0.6]}]}`))
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAIEmbeddingClientWithOptions(srv.URL, "embeddinggemma-2", "", 5*time.Second, 0, 1,
		"title: {title} | text: ", "task: search result | query: ")

	chunks := []*models.Chunk{
		{ID: "c1", Content: "first body", DocumentTitle: "Getting Started"},
		{ID: "c2", Content: "second body"},
	}
	_, err := client.GenerateChunkEmbeddings(context.Background(), chunks)
	require.NoError(t, err)

	require.Len(t, got.Input, 2)
	assert.Equal(t, "title: Getting Started | text: first body", got.Input[0],
		"the titled chunk must carry its document title")
	assert.Equal(t, "title: none | text: second body", got.Input[1],
		"a title-less chunk must be announced as title: none")
}
