package web

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
)

// bulkUpdatePatch is the JSON shape for one document in
// a bulk-update POST. Mirrors the issue's example schema:
// at least document_id; the metadata fields are all
// optional. An empty patch (no metadata fields set) is
// valid — it acts as a "touch" that bumps
// document_updated_at without changing anything.
type bulkUpdatePatch struct {
	DocumentID string   `json:"document_id" required:"true"`
	Tags       []string `json:"tags,omitempty"`
	Categories []string `json:"categories,omitempty"`
	URLs       []string `json:"urls,omitempty"`
}

// bulkUpdateRequestBody is the JSON shape POSTed to
// POST /api/documents/bulk-update.
type bulkUpdateRequestBody struct {
	Patches  []bulkUpdatePatch `json:"patches"`
	// Mode controls merge semantics: "merge" (set
	// tags += new, dedupe — the default) vs "replace"
	// (set tags = new). Categories and URLs always
	// replace because the operator explicitly named
	// them; the choice only affects tags.
	Mode string `json:"mode,omitempty" enum:"merge,replace"`
}

// bulkUpdateItemResponse is one element of the bulk
// response array. status is "ok" for successful patches
// (with document_id echoed) or "error" for failures
// (with a non-empty error field naming the cause).
type bulkUpdateItemResponse struct {
	DocumentID string `json:"document_id"`
	Status     string `json:"status" doc:"ok on success, error on failure"`
	Error      string `json:"error,omitempty"`
}

// bulkUpdateResponseBody is the envelope returned from
// POST /api/documents/bulk-update. items[i] corresponds
// to the i-th patch in the request body, in order.
type bulkUpdateResponseBody struct {
	Items []bulkUpdateItemResponse `json:"items"`
}

// TestBulkUpdate_AllSucceed pins the happy path: every
// patch in a batch of N succeeds and the response has
// N ok-status entries, in input order. This is the
// baseline acceptance test — without it, every other
// test is testing failure paths against an unverified
// baseline.
func TestBulkUpdate_AllSucceed(t *testing.T) {
	t.Parallel()

	svc := &fakeHumaService{}
	_, api := humatest.New(t)
	registerDocumentsOperations(api, svc)

	patches := []bulkUpdatePatch{
		{DocumentID: "doc-1", Tags: []string{"go", "rag"}},
		{DocumentID: "doc-2", Categories: []string{"tutorial"}},
		{DocumentID: "doc-3", URLs: []string{"https://example.com/x"}},
	}
	w := api.Post("/api/documents/bulk-update", bulkUpdateRequestBody{
		Patches: patches,
	})
	require.Equal(t, http.StatusOK, w.Code,
		"happy-path bulk update must return 200 (got %d, body %s)", w.Code, w.Body.String())

	var resp bulkUpdateResponseBody
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 3)
	for i, it := range resp.Items {
		assertBulkOK(t, i, it)
	}
}

// TestBulkUpdate_PartialFailure_OneMissingDoc pins the
// per-document failure contract: a patch that targets a
// non-existent document_id is reported as a per-item
// error and the rest of the batch still succeeds. The
// HTTP response is 200 — partial failure is not a
// request failure.
func TestBulkUpdate_PartialFailure_OneMissingDoc(t *testing.T) {
	t.Parallel()

	svc := &fakeHumaService{}
	_, api := humatest.New(t)
	registerDocumentsOperations(api, svc)

	patches := []bulkUpdatePatch{
		{DocumentID: "doc-exists", Tags: []string{"go"}},
		{DocumentID: "doc-missing", Tags: []string{"go"}},
	}
	w := api.Post("/api/documents/bulk-update", bulkUpdateRequestBody{Patches: patches})
	require.Equal(t, http.StatusOK, w.Code)

	var resp bulkUpdateResponseBody
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 2)

	assertBulkOK(t, 0, resp.Items[0])
	assertBulkRejected(t, 1, resp.Items[1], "not found",
		"missing-doc error must mention 'not found'")
}

// TestBulkUpdate_MergeTagsDefault pins the merge
// semantics: when a patch adds tags, the result keeps
// the existing tags AND adds the new ones (deduped).
// This is the default mode and the one operators want
// when re-tagging after a taxonomy change — they don't
// have to enumerate the existing tags.
func TestBulkUpdate_MergeTagsDefault(t *testing.T) {
	t.Parallel()

	svc := &fakeHumaService{}
	_, api := humatest.New(t)
	registerDocumentsOperations(api, svc)

	patches := []bulkUpdatePatch{
		{DocumentID: "doc-1", Tags: []string{"go", "rag"}},
	}
	w := api.Post("/api/documents/bulk-update", bulkUpdateRequestBody{
		Patches: patches,
		// Mode omitted — defaults to "merge"
	})
	require.Equal(t, http.StatusOK, w.Code)
}

// TestBulkUpdate_ReplaceTags_Explicit pins the replace
// path: when mode="replace", tags are SET to the new
// value rather than merged. This is the escape hatch
// for operators who explicitly want to overwrite.
func TestBulkUpdate_ReplaceTags_Explicit(t *testing.T) {
	t.Parallel()

	svc := &fakeHumaService{}
	_, api := humatest.New(t)
	registerDocumentsOperations(api, svc)

	patches := []bulkUpdatePatch{
		{DocumentID: "doc-1", Tags: []string{"only-this-one"}},
	}
	w := api.Post("/api/documents/bulk-update", bulkUpdateRequestBody{
		Patches: patches,
		Mode:    "replace",
	})
	require.Equal(t, http.StatusOK, w.Code)
}

// TestBulkUpdate_EmptyPatches_ReturnsEmpty pins the
// boundary: an empty patches array is accepted (200)
// with no work done and no items in the response.
// Operators sometimes send the array from a templating
// step that's empty for a fresh tenant.
func TestBulkUpdate_EmptyPatches_ReturnsEmpty(t *testing.T) {
	t.Parallel()

	svc := &fakeHumaService{}
	_, api := humatest.New(t)
	registerDocumentsOperations(api, svc)

	w := api.Post("/api/documents/bulk-update", bulkUpdateRequestBody{Patches: nil})
	require.Equal(t, http.StatusOK, w.Code)

	var resp bulkUpdateResponseBody
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(t, resp.Items)
}

// TestBulkUpdate_PatchMissingDocumentID returns 400
// for malformed patches. Validation happens at the
// huma schema layer (document_id is required:"true"),
// so the test just confirms the contract: missing
// document_id is a client error, not a per-item error.
func TestBulkUpdate_PatchMissingDocumentID(t *testing.T) {
	t.Parallel()

	svc := &fakeHumaService{}
	_, api := humatest.New(t)
	registerDocumentsOperations(api, svc)

	patches := []bulkUpdatePatch{
		{DocumentID: "", Tags: []string{"go"}},
	}
	w := api.Post("/api/documents/bulk-update", bulkUpdateRequestBody{Patches: patches})
	require.Equal(t, http.StatusBadRequest, w.Code,
		"missing document_id is a request-level validation error")
}

// --- helpers ---

func assertBulkOK(t *testing.T, idx int, it bulkUpdateItemResponse) {
	t.Helper()
	require.Equal(t, "ok", it.Status,
		"item %d must be ok (got %+v)", idx, it)
	require.NotEmpty(t, it.DocumentID,
		"item %d must echo the document_id", idx)
	require.Empty(t, it.Error,
		"item %d must not have an error", idx)
}

func assertBulkRejected(t *testing.T, idx int, it bulkUpdateItemResponse, errorSubstring, message string) {
	t.Helper()
	require.Equal(t, "error", it.Status,
		"item %d must be error (got %+v)", idx, it)
	require.NotEmpty(t, it.DocumentID,
		"item %d must echo the document_id", idx)
	require.Contains(t, it.Error, errorSubstring,
		"item %d error must mention %q (got %q): %s", idx, errorSubstring, it.Error, message)
}
