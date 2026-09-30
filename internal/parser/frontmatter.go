package parser

import "bytes"

// SplitDocbuilderFrontmatter splits a docbuilder document into
// its YAML frontmatter bytes and the markdown body. It is the
// lenient counterpart to DocbuilderParser.extractFrontmatter:
// returns ok=false (and the original content as markdown) when
// either the opening or closing `---` delimiter is missing,
// rather than returning an error. Callers that want strict
// validation (the ingest path) should keep using
// DocbuilderParser.ParseDocument; this helper is for callers
// that want to treat un-frontmattered content as "no existing
// fields to merge" rather than reject it.
//
// CRLF line endings (`\r\n` in the delimiters) are accepted as
// well as LF — pasted content from Windows or chat clients
// often arrives that way, and rejecting it silently loses the
// frontmatter. We normalize once at the top so the rest of the
// splitter stays LF-only.
func SplitDocbuilderFrontmatter(raw []byte) (frontmatter, markdown []byte, ok bool) {
	content := bytes.TrimSpace(raw)
	content = bytes.ReplaceAll(content, []byte("\r\n"), []byte("\n"))
	if !bytes.HasPrefix(content, []byte("---\n")) {
		return nil, content, false
	}
	rest := content[len("---\n"):]
	before, after, found := bytes.Cut(rest, []byte("\n---\n"))
	if !found {
		return nil, content, false
	}
	return bytes.TrimSpace(before), bytes.TrimSpace(after), true
}
