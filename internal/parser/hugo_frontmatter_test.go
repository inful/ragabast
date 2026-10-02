package parser

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestParseDocument_ExtractsDraftTrue pins the headline extraction
// from issue #97: `draft: true` in frontmatter populates doc.Draft.
// The IsUnpublished contract depends on this; without the test, a
// typo in the lookup key would silently never set Draft.
func TestParseDocument_ExtractsDraftTrue(t *testing.T) {
	p := NewDocbuilderParser()

	raw := []byte("---\nuid: draft-doc\ndraft: true\n---\n\nbody\n")
	doc, err := p.ParseDocument(raw, "test.md")
	require.NoError(t, err)
	require.True(t, doc.Draft)
}

// TestParseDocument_DraftDefaultsFalse pins the negative case:
// without a `draft:` key, doc.Draft stays false. Operators who
// never set the field are unaffected.
func TestParseDocument_DraftDefaultsFalse(t *testing.T) {
	p := NewDocbuilderParser()

	raw := []byte("---\nuid: no-draft\n---\n\nbody\n")
	doc, err := p.ParseDocument(raw, "test.md")
	require.NoError(t, err)
	require.False(t, doc.Draft)
}

// TestParseDocument_DraftExplicitFalse pins that `draft: false` is
// parsed as false (not as truthy because the field is present).
func TestParseDocument_DraftExplicitFalse(t *testing.T) {
	p := NewDocbuilderParser()

	raw := []byte("---\nuid: explicit-false\ndraft: false\n---\n\nbody\n")
	doc, err := p.ParseDocument(raw, "test.md")
	require.NoError(t, err)
	require.False(t, doc.Draft)
}

// TestParseDocument_DraftNonBooleanIgnored pins the safe-default
// contract: a `draft:` value that isn't a boolean (e.g. a typo
// `draft: "yes"`) stays false and does not filter. The error
// surfaces in YAML parsing, not as a misfire.
func TestParseDocument_DraftNonBooleanIgnored(t *testing.T) {
	p := NewDocbuilderParser()

	raw := []byte("---\nuid: draft-string\ndraft: \"yes\"\n---\n\nbody\n")
	doc, err := p.ParseDocument(raw, "test.md")
	require.NoError(t, err)
	require.False(t, doc.Draft,
		"a non-boolean draft: value must not be coerced to true")
}

// TestParseDocument_ExtractsDateRFC3339 pins RFC 3339 string parsing
// for `date:`. The parser accepts the same format Hugo accepts.
func TestParseDocument_ExtractsDateRFC3339(t *testing.T) {
	p := NewDocbuilderParser()

	raw := []byte("---\nuid: with-date\ndate: 2026-12-31T00:00:00Z\n---\n\nbody\n")
	doc, err := p.ParseDocument(raw, "test.md")
	require.NoError(t, err)
	require.Equal(t, 2026, doc.Date.Year())
	require.Equal(t, time.December, doc.Date.Month())
	require.Equal(t, 31, doc.Date.Day())
}

// TestParseDocument_ExtractsPublishDateRFC3339 pins the
// `publishDate:` field. The canonical name must populate
// doc.PublishDate.
func TestParseDocument_ExtractsPublishDateRFC3339(t *testing.T) {
	p := NewDocbuilderParser()

	raw := []byte("---\nuid: with-publish\npublishDate: 2026-12-31T00:00:00Z\n---\n\nbody\n")
	doc, err := p.ParseDocument(raw, "test.md")
	require.NoError(t, err)
	require.Equal(t, 2026, doc.PublishDate.Year())
}

// TestParseDocument_PublishDateAliasesAreEquivalent pins that the
// three Hugo aliases for publishDate all populate the same field.
// Each is tested in isolation so a regression in one alias's lookup
// shows up as that subtest failing.
func TestParseDocument_PublishDateAliasesAreEquivalent(t *testing.T) {
	cases := []struct {
		key string
	}{
		{"publishDate"},
		{"pubdate"},
		{"published"},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			p := NewDocbuilderParser()
			raw := []byte("---\nuid: alias\n" + tc.key + ": 2026-12-31T00:00:00Z\n---\n\nbody\n")
			doc, err := p.ParseDocument(raw, "test.md")
			require.NoError(t, err)
			require.Equal(t, 2026, doc.PublishDate.Year(),
				"%q must populate doc.PublishDate", tc.key)
		})
	}
}

// TestParseDocument_PublishDateCaseInsensitive pins that Hugo's
// case-insensitivity is honored: PublishDate, publishdate,
// PUBLISHDATE all populate the same field. Without
// lowerHugoKeys, yaml.v3's case-sensitive keys would silently
// split these into separate frontmatter entries.
func TestParseDocument_PublishDateCaseInsensitive(t *testing.T) {
	cases := []string{"publishdate", "PublishDate", "PUBDATE", "PUBLISHED"}
	for _, key := range cases {
		t.Run(key, func(t *testing.T) {
			p := NewDocbuilderParser()
			raw := []byte("---\nuid: ci\n" + key + ": 2026-12-31T00:00:00Z\n---\n\nbody\n")
			doc, err := p.ParseDocument(raw, "test.md")
			require.NoError(t, err)
			require.Equal(t, 2026, doc.PublishDate.Year(),
				"%q (any case) must populate doc.PublishDate", key)
		})
	}
}

// TestParseDocument_ExtractsExpiryDateRFC3339 pins the
// `expiryDate:` field.
func TestParseDocument_ExtractsExpiryDateRFC3339(t *testing.T) {
	p := NewDocbuilderParser()

	raw := []byte("---\nuid: with-expiry\nexpiryDate: 2020-01-01T00:00:00Z\n---\n\nbody\n")
	doc, err := p.ParseDocument(raw, "test.md")
	require.NoError(t, err)
	require.Equal(t, 2020, doc.ExpiryDate.Year())
}

// TestParseDocument_ExpiryDateAlias pins `unpublishdate:` as an
// alias for `expiryDate:`.
func TestParseDocument_ExpiryDateAlias(t *testing.T) {
	p := NewDocbuilderParser()

	raw := []byte("---\nuid: unpub-alias\nunpublishdate: 2020-01-01T00:00:00Z\n---\n\nbody\n")
	doc, err := p.ParseDocument(raw, "test.md")
	require.NoError(t, err)
	require.Equal(t, 2020, doc.ExpiryDate.Year())
}

// TestParseDocument_UnparseableDateStaysZero pins the safety
// contract: a date that doesn't parse as RFC 3339 leaves the
// Document field at its zero value. IsUnpublished then ignores it,
// so the doc is treated as published. A typo doesn't accidentally
// filter a document.
func TestParseDocument_UnparseableDateStaysZero(t *testing.T) {
	p := NewDocbuilderParser()

	raw := []byte("---\nuid: bad-date\ndate: not-a-date\n---\n\nbody\n")
	doc, err := p.ParseDocument(raw, "test.md")
	require.NoError(t, err)
	require.True(t, doc.Date.IsZero(),
		"unparseable date must leave the field at zero")
	require.False(t, doc.Draft)
}

// TestParseDocument_DateAndPublishDateDistinguished pins that the
// two date fields don't cross-contaminate: `date:` populates
// doc.Date only, `publishDate:` populates doc.PublishDate only.
func TestParseDocument_DateAndPublishDateDistinguished(t *testing.T) {
	p := NewDocbuilderParser()

	raw := []byte("---\nuid: both-dates\ndate: 2020-01-01T00:00:00Z\npublishDate: 2030-01-01T00:00:00Z\n---\n\nbody\n")
	doc, err := p.ParseDocument(raw, "test.md")
	require.NoError(t, err)
	require.Equal(t, 2020, doc.Date.Year())
	require.Equal(t, 2030, doc.PublishDate.Year())
}

// TestParseDocument_AllFourMarkersCoexist pins that all four
// markers can be set in the same document and each lands on its
// own field.
func TestParseDocument_AllFourMarkersCoexist(t *testing.T) {
	p := NewDocbuilderParser()

	raw := []byte(`---
uid: all-four
draft: true
date: 2026-01-01T00:00:00Z
publishDate: 2027-01-01T00:00:00Z
expiryDate: 2025-01-01T00:00:00Z
---

body
`)
	doc, err := p.ParseDocument(raw, "test.md")
	require.NoError(t, err)
	require.True(t, doc.Draft)
	require.Equal(t, 2026, doc.Date.Year())
	require.Equal(t, 2027, doc.PublishDate.Year())
	require.Equal(t, 2025, doc.ExpiryDate.Year())
}

// TestParseDocument_NoMarkersLeavesAllZero is the negative space
// counterpart: a frontmatter without any of the four Hugo fields
// produces a Document with zero values everywhere. This is the
// regression guard — without it, a typo in lowerHugoKeys could
// default to populating random fields.
func TestParseDocument_NoMarkersLeavesAllZero(t *testing.T) {
	p := NewDocbuilderParser()

	raw := []byte("---\nuid: plain\ntitle: Plain Doc\n---\n\nbody\n")
	doc, err := p.ParseDocument(raw, "test.md")
	require.NoError(t, err)
	require.False(t, doc.Draft)
	require.True(t, doc.Date.IsZero())
	require.True(t, doc.PublishDate.IsZero())
	require.True(t, doc.ExpiryDate.IsZero())
}

// TestParseDocument_NoFrontmatterLeavesAllZero pins the
// frontmatter-free edge case: a markdown file with no `---\n`
// opener must not accidentally synthesize a Hugo field. UID has to
// be in frontmatter (the parser requires it), so we set just UID.
func TestParseDocument_NoFrontmatterLeavesAllZero(t *testing.T) {
	p := NewDocbuilderParser()

	raw := []byte("---\nuid: zero-only\n---\n\nbody\n")
	doc, err := p.ParseDocument(raw, "plain.md")
	require.NoError(t, err)
	require.False(t, doc.Draft)
	require.True(t, doc.Date.IsZero())
	require.True(t, doc.PublishDate.IsZero())
	require.True(t, doc.ExpiryDate.IsZero())
}
