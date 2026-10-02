package parser

import (
	"testing"
	"time"

	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/require"
)

// fixedNow is the reference point every IsUnpublished test anchors
// against. Use 2026-06-15 12:00:00 UTC — comfortably away from any
// month/year boundaries that might trip a timezone-related edge case.
var fixedNow = time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

// TestIsUnpublished_NilDocIsPublished pins the safe default:
// IsUnpublished(nil) == false. A nil document is a programmer error,
// not an unpublished one; the helper should not crash, and it should
// not return "yes, delete it" because there's nothing to filter.
func TestIsUnpublished_NilDocIsPublished(t *testing.T) {
	require.False(t, IsUnpublished(nil, fixedNow))
}

// TestIsUnpublished_EmptyDocIsPublished pins the "no markers set"
// happy path: a parsed document with zero values on every Hugo field
// is published.
func TestIsUnpublished_EmptyDocIsPublished(t *testing.T) {
	doc := &models.Document{}
	require.False(t, IsUnpublished(doc, fixedNow))
}

// TestIsUnpublished_DraftTrueIsUnpublished pins the headline case
// from issue #97: `draft: true` ⇒ unpublished.
func TestIsUnpublished_DraftTrueIsUnpublished(t *testing.T) {
	doc := &models.Document{Draft: true}
	require.True(t, IsUnpublished(doc, fixedNow))
}

// TestIsUnpublished_DraftFalseIsPublished pins the negative case:
// `draft: false` (or omitted) does not filter the document.
func TestIsUnpublished_DraftFalseIsPublished(t *testing.T) {
	doc := &models.Document{Draft: false}
	require.False(t, IsUnpublished(doc, fixedNow))
}

// TestIsUnpublished_DateInFutureIsUnpublished pins the second Hugo
// rule: `date: <future>` ⇒ unpublished. The date is set to one hour
// past the reference now.
func TestIsUnpublished_DateInFutureIsUnpublished(t *testing.T) {
	doc := &models.Document{Date: fixedNow.Add(time.Hour)}
	require.True(t, IsUnpublished(doc, fixedNow))
}

// TestIsUnpublished_DateAtNowIsPublished pins the boundary: a date
// exactly equal to now is not "in the future". Hugo's behavior is
// exclusive: future means strictly later.
func TestIsUnpublished_DateAtNowIsPublished(t *testing.T) {
	doc := &models.Document{Date: fixedNow}
	require.False(t, IsUnpublished(doc, fixedNow))
}

// TestIsUnpublished_DateInPastIsPublished pins the past case: a
// date that has already happened does not filter. This is the
// common case — most documents have a `date:` in the past.
func TestIsUnpublished_DateInPastIsPublished(t *testing.T) {
	doc := &models.Document{Date: fixedNow.Add(-24 * time.Hour)}
	require.False(t, IsUnpublished(doc, fixedNow))
}

// TestIsUnpublished_PublishDateInFutureIsUnpublished pins the
// third Hugo rule. The alias lookup is in the parser layer; this
// helper just sees the populated struct field.
func TestIsUnpublished_PublishDateInFutureIsUnpublished(t *testing.T) {
	doc := &models.Document{PublishDate: fixedNow.Add(time.Hour)}
	require.True(t, IsUnpublished(doc, fixedNow))
}

// TestIsUnpublished_PublishDateAtNowIsPublished pins the boundary
// for publishDate, mirroring the date boundary.
func TestIsUnpublished_PublishDateAtNowIsPublished(t *testing.T) {
	doc := &models.Document{PublishDate: fixedNow}
	require.False(t, IsUnpublished(doc, fixedNow))
}

// TestIsUnpublished_PublishDateInPastIsPublished pins the past case
// for publishDate: a publishDate in the past is published. This is
// the "stale future marker" case where the date was set forward
// during a draft cycle and the operator never reset it.
func TestIsUnpublished_PublishDateInPastIsPublished(t *testing.T) {
	doc := &models.Document{PublishDate: fixedNow.Add(-time.Hour)}
	require.False(t, IsUnpublished(doc, fixedNow))
}

// TestIsUnpublished_ExpiryDateInPastIsUnpublished pins the fourth
// Hugo rule: `expiryDate: <past>` ⇒ unpublished. This is the "take
// down this post" case.
func TestIsUnpublished_ExpiryDateInPastIsUnpublished(t *testing.T) {
	doc := &models.Document{ExpiryDate: fixedNow.Add(-time.Hour)}
	require.True(t, IsUnpublished(doc, fixedNow))
}

// TestIsUnpublished_ExpiryDateAtNowIsPublished pins the boundary:
// an expiry exactly at now is not yet expired.
func TestIsUnpublished_ExpiryDateAtNowIsPublished(t *testing.T) {
	doc := &models.Document{ExpiryDate: fixedNow}
	require.False(t, IsUnpublished(doc, fixedNow))
}

// TestIsUnpublished_ExpiryDateInFutureIsPublished pins the future
// case for expiryDate: an expiry in the future does not filter. (The
// document is still meant to be live until then.)
func TestIsUnpublished_ExpiryDateInFutureIsPublished(t *testing.T) {
	doc := &models.Document{ExpiryDate: fixedNow.Add(24 * time.Hour)}
	require.False(t, IsUnpublished(doc, fixedNow))
}

// TestIsUnpublished_ZeroDatesAreIgnored pins the "unparseable date"
// safety: a date field that comes out as zero (parse failure in the
// parser layer) does not filter. The contract is "be conservative
// and let things through".
func TestIsUnpublished_ZeroDatesAreIgnored(t *testing.T) {
	doc := &models.Document{
		// Date, PublishDate, ExpiryDate all zero
	}
	require.False(t, IsUnpublished(doc, fixedNow))
}

// TestIsUnpublished_CombinedMarkersAreORed pins the OR semantics:
// if any single marker fires, the document is unpublished. We don't
// require all four to fire.
func TestIsUnpublished_CombinedMarkersAreORed(t *testing.T) {
	cases := []struct {
		name string
		doc  *models.Document
	}{
		{"draft only", &models.Document{Draft: true}},
		{"date only", &models.Document{Date: fixedNow.Add(time.Hour)}},
		{"publishDate only", &models.Document{PublishDate: fixedNow.Add(time.Hour)}},
		{"expiryDate only", &models.Document{ExpiryDate: fixedNow.Add(-time.Hour)}},
		// Both draft AND future date — still unpublished (either would suffice).
		{"draft and future date", &models.Document{Draft: true, Date: fixedNow.Add(time.Hour)}},
		// Future date AND past date — future wins because Hugo treats
		// date as a hard publish-time gate independent of expiry.
		{"future date and past expiry", &models.Document{
			Date:       fixedNow.Add(time.Hour),
			ExpiryDate: fixedNow.Add(-time.Hour),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.True(t, IsUnpublished(tc.doc, fixedNow),
				"%s: at least one Hugo marker fired, expected unpublished", tc.name)
		})
	}
}

// TestIsUnpublished_AllFourMarkersUnsetIsPublished pins the "none
// of the markers fired" case: a document with all four zero values
// is published. This is the regression guard — without this test, a
// typo in `if !doc.Date.IsZero()` could default to "everything is
// unpublished" and the database would silently lose every re-ingest.
func TestIsUnpublished_AllFourMarkersUnsetIsPublished(t *testing.T) {
	doc := &models.Document{
		Draft:       false,
		Date:        time.Time{},
		PublishDate: time.Time{},
		ExpiryDate:  time.Time{},
	}
	require.False(t, IsUnpublished(doc, fixedNow))
}
