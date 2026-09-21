package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFormatFrom covers the case-insensitive mapping from the
// env-var string to the Format constant. Unknown values fall
// back to FormatText so a typo doesn't silently switch the
// whole process to JSON.
func TestFormatFrom(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want Format
	}{
		{"empty", "", FormatText},
		{"text explicit", "text", FormatText},
		{"TEXT uppercase", "TEXT", FormatText},
		{"json lowercase", "json", FormatJSON},
		{"JSON uppercase", "JSON", FormatJSON},
		{"Json mixed case", "Json", FormatJSON},
		{"with whitespace", "  json  ", FormatJSON},
		{"unknown value", "yaml", FormatText},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, FormatFrom(tc.in))
		})
	}
}

// TestInitWith_JSONOutput pins the contract from issue #21's
// acceptance criteria: when RAGABAST_LOG_FORMAT=json, every
// slog line is a valid JSON object with the configured fields.
// The test captures the writer's output and asserts both the
// per-line structure and the message+level fields.
func TestInitWith_JSONOutput(t *testing.T) {
	buf := &bytes.Buffer{}
	InitWith("json", buf)

	slog.Info("test message", "key1", "value1", "key2", 42)

	line := strings.TrimSpace(buf.String())
	require.NotEmpty(t, line, "logger must write something")

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(line), &got),
		"JSON-format output must be a single valid JSON object; got: %s", line)

	assert.Equal(t, "test message", got["msg"])
	assert.Equal(t, "INFO", got["level"])
	assert.Equal(t, "value1", got["key1"])
	// JSON numbers come back as float64
	assert.InDelta(t, 42.0, got["key2"], 0.001)
}

// TestInitWith_TextOutputDefault covers the default format:
// when no env value is set (or it's something we don't
// recognize), the output is text. We don't pin the exact
// slog.NewTextHandler format — that's slog's contract — but
// we do pin that the message and the field values are both
// present and human-readable. Operators grep for these.
func TestInitWith_TextOutputDefault(t *testing.T) {
	buf := &bytes.Buffer{}
	InitWith("", buf)

	slog.Info("test message", "key1", "value1", "key2", 42)

	line := buf.String()
	require.NotEmpty(t, line)

	// Text handler emits key=value pairs separated by spaces;
	// we pin that the operator-grep targets are present.
	assert.Contains(t, line, "test message", "the message itself is in the line")
	assert.Contains(t, line, "key1=value1", "fields render as key=value in text mode")
	assert.Contains(t, line, "key2=42", "numeric fields render without quotes in text mode")
	assert.NotContains(t, line, `"msg":`,
		"text mode must not emit JSON quotes; got: %s", line)
}

// TestInitWith_TextOutputOverridesUnknown pins that an
// unrecognized env value falls back to text rather than
// crashing or emitting some other format. The default is
// intentionally forgiving so an operator typo doesn't
// silently switch the whole process.
func TestInitWith_TextOutputOverridesUnknown(t *testing.T) {
	buf := &bytes.Buffer{}
	InitWith("yaml", buf)

	slog.Info("ignored-format", "k", "v")

	line := buf.String()
	assert.Contains(t, line, "ignored-format")
	assert.NotContains(t, line, `"msg":`,
		"unknown format must default to text; got: %s", line)
}
