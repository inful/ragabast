package parser

import (
	"testing"

	"github.com/ragabast/internal/models"
	"github.com/stretchr/testify/require"
)

// TestParseDocument_MarksContentFailuresAsInvalidDocument pins
// the error taxonomy the HTTP layer maps to 400: a document the
// server refuses because of its content is models.ErrInvalidDocument,
// so the caller can tell it apart from a server-side failure
// (500). The message must survive the wrapping — operators read
// it.
func TestParseDocument_MarksContentFailuresAsInvalidDocument(t *testing.T) {
	p := NewDocbuilderParser()

	cases := []struct {
		name string
		raw  string
	}{
		{"unterminated frontmatter", "---\nuid: sample\n\n# Title\nHello\n"},
		{"invalid yaml", "---\nuid: [unclosed\n---\n\n# Title\nHello\n"},
		{"missing uid", "---\ntitle: t\n---\n\n# Title\nHello\n"},
		{"empty content", "---\nuid: sample\n---\n\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := p.ParseDocument([]byte(tc.raw), "test.md")
			require.Error(t, err)
			require.ErrorIs(t, err, models.ErrInvalidDocument)
		})
	}
}

// TestParseDocument_FrontmatterAtEndOfFile pins the case a real
// corpus hit: a document whose frontmatter runs to the end of the
// file — no body, and no trailing newline after the closing "---".
//
// The parser trimmed the trailing whitespace and then looked for
// "\n---\n", so the closing delimiter was never found and the file
// was rejected with "frontmatter delimiter not found" even though
// it was well formed.
func TestParseDocument_FrontmatterAtEndOfFile(t *testing.T) {
	p := NewDocbuilderParser()

	raw := []byte("---\nuid: sample\ntitle: Sample\n---")
	_, err := p.ParseDocument(raw, "test.md")
	require.Error(t, err, "a document with no body has no content to index")
	require.ErrorIs(t, err, models.ErrMissingContent,
		"the frontmatter must still be parsed — the failure is the missing body, not a missing delimiter")

	// With a body, the same shape parses cleanly.
	raw = []byte("---\nuid: sample\ntitle: Sample\n---\n\n# Title\nHello\n")
	doc, err := p.ParseDocument(raw, "test.md")
	require.NoError(t, err)
	require.Equal(t, "sample", doc.UID)
	require.Equal(t, "Sample", doc.Title)
	require.Equal(t, "# Title\nHello", doc.Content)
}

// TestParseDocument_StillRejectsUnterminatedFrontmatter pins that
// the relaxed end-of-file rule did not weaken the strict one: a
// file that opens frontmatter and never closes it is still an
// error.
func TestParseDocument_StillRejectsUnterminatedFrontmatter(t *testing.T) {
	p := NewDocbuilderParser()

	_, err := p.ParseDocument([]byte("---\nuid: sample\n\n# Title\nHello\n"), "test.md")
	require.Error(t, err)
	require.Contains(t, err.Error(), "frontmatter delimiter not found")
}

func TestParseDocument_GeneratesFingerprintAndStableID(t *testing.T) {
	p := NewDocbuilderParser()

	raw := []byte("---\nfingerprint: \"auto-generated-if-empty\"\nuid: sample\nurls:\n  - https://example.com\n---\n\n# Title\nHello\n")

	doc, err := p.ParseDocument(raw, "test.md")
	require.NoError(t, err)
	require.Equal(t, "sample", doc.ID)
	require.Equal(t, "sample", doc.UID)
	require.NotEmpty(t, doc.Fingerprint)
	require.NotEmpty(t, doc.Fingerprint, "fingerprint should be populated even when not in frontmatter")
}

func TestParseDocument_PreservesExplicitFingerprintAndStableID(t *testing.T) {
	p := NewDocbuilderParser()

	fp := "b7add053acff6f4d1f5a7b6e66f7d6e6a8e2d9d8b1f956c534027be0f41fd3f9"
	raw := []byte("---\nfingerprint: " + fp + "\nuid: sample\nurls:\n  - https://example.com\n---\n\n# Title\nHello\n")

	doc, err := p.ParseDocument(raw, "test.md")
	require.NoError(t, err)
	require.Equal(t, "sample", doc.ID)
	require.Equal(t, "sample", doc.UID)
	require.Equal(t, fp, doc.Fingerprint)
}

func TestParseDocument_AllowsMissingURLs(t *testing.T) {
	p := NewDocbuilderParser()

	fp := "b7add053acff6f4d1f5a7b6e66f7d6e6a8e2d9d8b1f956c534027be0f41fd3f9"
	raw := []byte("---\nfingerprint: " + fp + "\nuid: sample\n---\n\n# Title\nHello\n")

	doc, err := p.ParseDocument(raw, "test.md")
	require.NoError(t, err)
	require.Equal(t, "sample", doc.ID)
	require.Empty(t, doc.URLs)
}

// TestParseDocument_CRLFFrontmatter pins the headline fix for
// issue #82: docbuilder content pasted from Windows or any
// CRLF-emitting source (chat clients, browser textareas that
// preserve the user's paste) must produce the same parsed
// document as LF content. The browser submit path sends
// `---\r\nuid:...\r\n---\r\n`; before this fix the parser failed
// to recognize the opening delimiter and treated the whole
// input as markdown, leaving doc.UID empty and triggering the
// 500 "document UID is required" error on the web ingest path.
//
// The test also covers CRLF in the frontmatter `title:` field
// — strict frontmatter title precedence (see
// TestParseDocument_ExtractsTitleFromFrontmatter) means a
// CRLF past of the title line must still produce the parsed
// title without a trailing \r.
func TestParseDocument_CRLFFrontmatter(t *testing.T) {
	p := NewDocbuilderParser()

	raw := []byte("---\r\nuid: crlf-doc\r\nfingerprint: crlf-doc-v1\r\ntitle: Hello\r\n---\r\n\r\n# Body H1\r\n\r\nbody\r\n")

	doc, err := p.ParseDocument(raw, "test.md")
	require.NoError(t, err)
	require.Equal(t, "crlf-doc", doc.UID)
	require.Equal(t, "crlf-doc", doc.ID)
	require.Equal(t, "crlf-doc-v1", doc.Fingerprint)
	require.Equal(t, "Hello", doc.Title,
		"frontmatter title must extract correctly when the frontmatter uses CRLF line endings")
}

// TestParseDocument_ExtractsTitleFromFrontmatter pins the
// happy path for the strict frontmatter-title contract: a
// frontmatter `title:` field is parsed and stored on
// doc.Title. This is the primary source of truth for the
// document's display label.
func TestParseDocument_ExtractsTitleFromFrontmatter(t *testing.T) {
	p := NewDocbuilderParser()

	raw := []byte("---\nuid: adr-001\ntitle: ADR 001 -- Use Postgres\n---\n\n# Anything\n\nbody\n")

	doc, err := p.ParseDocument(raw, "test.md")
	require.NoError(t, err)
	require.Equal(t, "ADR 001 -- Use Postgres", doc.Title,
		"frontmatter title must be parsed and stored on doc.Title")
}

// TestParseDocument_TitleFromFrontmatterIgnoresH1 pins the
// strict precedence rule: when both a frontmatter `title:`
// AND a `# H1` are present, the frontmatter value wins and
// the H1 is silently ignored. A contributor cannot "fix" an
// empty frontmatter title by adding a body H1 — the body
// H1 is no longer consulted for title extraction.
func TestParseDocument_TitleFromFrontmatterIgnoresH1(t *testing.T) {
	p := NewDocbuilderParser()

	raw := []byte("---\nuid: adr-002\ntitle: From Frontmatter\n---\n\n# From H1\n\nbody\n")

	doc, err := p.ParseDocument(raw, "test.md")
	require.NoError(t, err)
	require.Equal(t, "From Frontmatter", doc.Title,
		"frontmatter title must win over the body H1; H1 is no longer consulted for title extraction")
}

// TestParseDocument_NoTitleInFrontmatterEmptyTitle pins the
// regression: when no `title:` is in the frontmatter, the
// document title is empty — even if a `# H1` is present in
// the body. The old H1-fallback path is gone. An empty
// doc.Title flows through DisplayLabel's filename and ID
// fallbacks at the presentation layer.
func TestParseDocument_NoTitleInFrontmatterEmptyTitle(t *testing.T) {
	p := NewDocbuilderParser()

	raw := []byte("---\nuid: adr-003\n---\n\n# Should Be Ignored\n\nbody\n")

	doc, err := p.ParseDocument(raw, "test.md")
	require.NoError(t, err)
	require.Empty(t, doc.Title,
		"without a frontmatter title, doc.Title must be empty; the H1 fallback was removed")
}

// TestParseDocument_FrontmatterTitleWhitespaceTrimmed pins
// that whitespace around the frontmatter title is trimmed,
// matching how every other frontmatter string field behaves
// (UID, fingerprint). A title like "  Foo  " is stored as
// "Foo".
func TestParseDocument_FrontmatterTitleWhitespaceTrimmed(t *testing.T) {
	p := NewDocbuilderParser()

	raw := []byte("---\nuid: adr-004\ntitle:   Padded Title   \n---\n\nbody\n")

	doc, err := p.ParseDocument(raw, "test.md")
	require.NoError(t, err)
	require.Equal(t, "Padded Title", doc.Title,
		"frontmatter title must be trimmed of leading and trailing whitespace")
}

// TestParseDocument_CRLFPreservesRawContent ensures the fix
// normalizes CRLF only for parsing, not for the stored RawContent
// field. RawContent is what gets re-ingested later and what
// operators see in logs/diagnostics; rewriting it would hide the
// original bytes.
func TestParseDocument_CRLFPreservesRawContent(t *testing.T) {
	p := NewDocbuilderParser()

	raw := []byte("---\r\nuid: raw-cr-preserved\r\n---\r\nbody\r\n")

	doc, err := p.ParseDocument(raw, "test.md")
	require.NoError(t, err)
	require.Equal(t, raw, doc.RawContent,
		"RawContent must be the original bytes, not the normalized form")
}

// TestParseDocument_SourceKindFromFrontmatter pins that the
// gitlab writer's `source_kind: gitlab` line lands on
// models.Document.SourceKind via the typed extraction. Without
// this, every gitlab-ingested doc would deserialize back as
// SourceUnknown, and the citation dispatch + post-filter would
// treat it as a docbuilder doc (the legacy default) —
// specifically, gitlab citations would resolve to the
// docbuilder permalink instead of the original GitLab URL.
func TestParseDocument_SourceKindFromFrontmatter(t *testing.T) {
	p := NewDocbuilderParser()
	raw := []byte(`---
uid: sample
source_kind: gitlab
---

# Title

Hello
`)
	doc, err := p.ParseDocument(raw, "test.md")
	require.NoError(t, err)
	require.Equal(t, "gitlab", string(doc.SourceKind),
		"source_kind: gitlab must populate doc.SourceKind verbatim")
}

// TestParseDocument_UntypedFrontmatterGoesToMetadata pins the
// generic "extra fields → doc.Metadata" pass. The gitlab writer
// emits source-specific keys (state, author_username) that the
// parser doesn't recognize as typed fields; they must flow
// through to doc.Metadata so the ingest pipeline can copy them
// to chunk.Metadata. If the parser dropped unknown keys, every
// gitlab-sourced chunk would lose its state filter.
func TestParseDocument_UntypedFrontmatterGoesToMetadata(t *testing.T) {
	p := NewDocbuilderParser()
	raw := []byte(`---
uid: sample
source_kind: gitlab
state: opened
author_username: alice
---

# Title

Hello
`)
	doc, err := p.ParseDocument(raw, "test.md")
	require.NoError(t, err)
	require.Equal(t, "opened", doc.Metadata["state"],
		"untyped 'state' frontmatter key must flow through to doc.Metadata")
	require.Equal(t, "alice", doc.Metadata["author_username"],
		"untyped 'author_username' frontmatter key must flow through to doc.Metadata")
}

// TestParseDocument_TypedKeysAreNotCopiedToMetadata pins the
// "no double-storage" rule: a frontmatter key that IS typed
// (uid, title, tags, urls, dates, etc.) must NOT also land in
// doc.Metadata. Without this, chunk metadata would carry the
// title as both doc.Title (typed) and doc.Metadata["title"]
// (string), wasting space and confusing downstream filters.
func TestParseDocument_TypedKeysAreNotCopiedToMetadata(t *testing.T) {
	p := NewDocbuilderParser()
	raw := []byte(`---
uid: sample
title: My Document
tags:
  - foo
urls:
  - https://example.com
state: opened
---

# Title

Hello
`)
	doc, err := p.ParseDocument(raw, "test.md")
	require.NoError(t, err)
	require.Equal(t, "My Document", doc.Title,
		"sanity: typed title extraction still applies")
	_, hasUID := doc.Metadata["uid"]
	_, hasTitle := doc.Metadata["title"]
	_, hasTags := doc.Metadata["tags"]
	_, hasURLs := doc.Metadata["urls"]
	require.False(t, hasUID, "typed 'uid' must NOT be copied to Metadata")
	require.False(t, hasTitle, "typed 'title' must NOT be copied to Metadata")
	require.False(t, hasTags, "typed 'tags' must NOT be copied to Metadata (it's a list, not a string)")
	require.False(t, hasURLs, "typed 'urls' must NOT be copied to Metadata (it's a list, not a string)")
	require.Equal(t, "opened", doc.Metadata["state"],
		"untyped 'state' must still flow to Metadata even when typed keys are present")
}

// TestParseDocument_NoMetadataWhenFrontmatterIsOnlyTyped pins the
// empty-bag rule: a doc whose frontmatter contains only typed
// keys must have doc.Metadata == nil (not an empty map). The
// ingest pipeline uses `len(doc.Metadata) > 0` to decide whether
// to copy; an empty map would still be > 0 in some languages
// but Go's len works on nil maps too — both are zero-length. The
// stricter guarantee here is the JSON/BSON serialization shape:
// nil maps serialize as nothing (omitempty), empty maps serialize
// as {}.
func TestParseDocument_NoMetadataWhenFrontmatterIsOnlyTyped(t *testing.T) {
	p := NewDocbuilderParser()
	raw := []byte("---\nuid: sample\ntitle: Plain\n---\n\n# Title\n\nBody\n")
	parsed, err := p.ParseDocument(raw, "test.md")
	require.NoError(t, err)
	require.Nil(t, parsed.Metadata,
		"docs with no untyped frontmatter keys must have nil Metadata so omitempty drops the BSON/JSON field")
}

// TestParseDocument_ExtractsWebSources pins the request side of
// the web-mirror feature: a document names the external pages it
// wants mirrored through its `web_sources:` frontmatter.
//
// The request is the author's decision; whether ragabast may fetch
// it is the operator's (the allowed-hosts policy). This test only
// pins that the request survives the parse.
func TestParseDocument_ExtractsWebSources(t *testing.T) {
	p := NewDocbuilderParser()

	raw := []byte("---\nuid: sample\nweb_sources:\n  - https://example.com/spec\n  - https://example.com/other\n---\n\n# Title\nHello\n")
	doc, err := p.ParseDocument(raw, "test.md")
	require.NoError(t, err)
	require.Equal(t, []string{"https://example.com/spec", "https://example.com/other"}, doc.WebSources)
}

// TestParseDocument_NoWebSourcesIsEmpty pins that a document that
// requests nothing has no requests — the mirroring path must not
// treat an absent field as an empty request.
func TestParseDocument_NoWebSourcesIsEmpty(t *testing.T) {
	p := NewDocbuilderParser()

	raw := []byte("---\nuid: sample\n---\n\n# Title\nHello\n")
	doc, err := p.ParseDocument(raw, "test.md")
	require.NoError(t, err)
	require.Empty(t, doc.WebSources)
}
