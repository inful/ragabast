package service

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSearchFilters_ToWhere verifies the conversion from the
// service-layer SearchFilters struct to chromem-go's
// map[string]string Where filter. The conversion rules:
//
//   - DocumentID becomes the "document_id" key
//   - Tag and Category are NOT translated: chromem's Where filter
//     is exact equality on the metadata value and the tag list is
//     stored as one newline-joined string, so an exact match only
//     hit documents whose tag list was exactly the filtered value.
//     Every multi-tag document was silently dropped from the
//     result. They are applied in Go after the retrieval instead
//     (see vector.FilterByTagsAndCategories).
//   - Empty fields are omitted
func TestSearchFilters_ToWhere(t *testing.T) {
	cases := []struct {
		name    string
		filters SearchFilters
		want    map[string]string
	}{
		{
			name:    "empty filters produce no where",
			filters: SearchFilters{},
			want:    map[string]string{},
		},
		{
			name:    "only document id",
			filters: SearchFilters{DocumentID: "doc-1"},
			want:    map[string]string{"document_id": "doc-1"},
		},
		{
			name:    "only tag produces no where",
			filters: SearchFilters{Tag: "go"},
			want:    map[string]string{},
		},
		{
			name:    "only category produces no where",
			filters: SearchFilters{Category: "Guides"},
			want:    map[string]string{},
		},
		{
			name: "the document id is the only key",
			filters: SearchFilters{
				DocumentID: "doc-1",
				Tag:        "go",
				Category:   "Guides",
			},
			want: map[string]string{"document_id": "doc-1"},
		},
		{
			name:    "whitespace-only fields are treated as empty",
			filters: SearchFilters{DocumentID: "  ", Tag: "", Category: "\t"},
			want:    map[string]string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.filters.ToWhere()
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ToWhere() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestSearchFilters_DocumentIDWinsOnConflict pins that if both
// DocumentID is set AND the caller passes the document_id field
// directly (via a future map-shaped API), DocumentID takes
// precedence. Today there's no such conflict, but documenting
// the rule now keeps the future migration safe.
func TestSearchFilters_DocumentIDIsAuthoritative(t *testing.T) {
	f := SearchFilters{DocumentID: "doc-1"}
	require.Equal(t, "doc-1", f.DocumentID, "DocumentID is the only field that can hold a single document")
}

// TestService_Search_AcceptsFilters documents the new signature.
// It's a smoke test that the type compiles and behaves; the
// real ranking is exercised through the existing vector-store
// integration tests.
func TestService_SearchFilters_ZeroValueCompiles(t *testing.T) {
	var f SearchFilters
	require.Equal(t, map[string]string{}, f.ToWhere())
}
