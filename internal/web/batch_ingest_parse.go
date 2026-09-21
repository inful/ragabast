package web

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
)

// parseIngestBatchBody parses the raw body of a batch
// ingest request into a slice of items. Two formats are
// supported and the dispatcher figures out which one the
// caller used by looking at the first non-whitespace byte:
//
//   - '[' at the start: a JSON array (Content-Type is
//     typically application/json).
//   - '{' (or anything else that isn't '['): NDJSON, one
//     JSON object per line (Content-Type is typically
//     application/x-ndjson).
//
// We dispatch by body shape rather than Content-Type so
// the endpoint works for both formats without requiring a
// specific Content-Type header — this matches the
// docbuilder pipeline's behavior where clients sometimes
// forget to set the header and the body shape is the only
// reliable signal.
func parseIngestBatchBody(raw []byte) ([]ingestBatchItem, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, nil
	}
	switch trimmed[0] {
	case '[':
		return parseJSONArray(trimmed)
	case '{':
		return parseNDJSON(trimmed)
	default:
		return nil, fmt.Errorf("body must start with '[' (JSON array) or '{' (NDJSON line); got %q", trimmed[0])
	}
}

// parseJSONArray parses the JSON-array variant of the
// batch body. Returns an error on a malformed body so the
// handler can surface a 400 to the caller.
func parseJSONArray(raw []byte) ([]ingestBatchItem, error) {
	var items []ingestBatchItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("expected JSON array: %w", err)
	}
	return items, nil
}

// parseNDJSON parses the NDJSON variant — one JSON object
// per line. Malformed lines produce an error so the
// caller can fix the source. We don't try to skip and
// continue: an unparseable line is almost always a bug
// worth surfacing, not a recoverable partial failure.
//
// Empty lines (whitespace-only) are skipped — common in
// streamed exports that flush a final newline.
func parseNDJSON(raw []byte) ([]ingestBatchItem, error) {
	var items []ingestBatchItem
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	// Generous buffer for very long lines (docbuilder
	// documents can exceed the 64K scanner default).
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var item ingestBatchItem
		if err := json.Unmarshal(line, &item); err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNum, err)
		}
		items = append(items, item)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan ndjson: %w", err)
	}
	return items, nil
}

// (placeholder removed — package compiles without
// extra anchor now that batch_ingest_parse.go's exported
// symbols are all referenced from production code.)
