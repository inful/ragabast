package service

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWalkMarkdownFiles_ProcessesOnlyMarkdown verifies the walker
// filters out non-*.md files and counts successes.
func TestWalkMarkdownFiles_ProcessesOnlyMarkdown(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.md"), []byte("# a"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.md"), []byte("# b"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ignore.txt"), []byte("nope"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README"), []byte("nope"), 0o600))

	var seen []string
	result, err := walkMarkdownFiles(dir, func(path string) error {
		seen = append(seen, filepath.Base(path))
		return nil
	})

	require.NoError(t, err)
	require.Equal(t, 2, result.Processed)
	require.Equal(t, 0, result.Failed)
	require.Empty(t, result.Errors)
	sort.Strings(seen)
	require.Equal(t, []string{"a.md", "b.md"}, seen)
}

// TestWalkMarkdownFiles_RecordsFailures verifies that ingest errors
// land in IngestResult.Errors and bump Failed without aborting the
// walk — the remaining files still get a chance to succeed.
func TestWalkMarkdownFiles_RecordsFailures(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ok.md"), []byte("# ok"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.md"), []byte("# bad"), 0o600))

	wantErr := errors.New("simulated ingest failure")
	var attempts []string
	result, err := walkMarkdownFiles(dir, func(path string) error {
		attempts = append(attempts, filepath.Base(path))
		if filepath.Base(path) == "bad.md" {
			return wantErr
		}
		return nil
	})

	require.NoError(t, err, "directory walk itself should not fail when individual files fail")
	require.Len(t, attempts, 2, "every markdown file should be attempted")
	require.Equal(t, 1, result.Processed)
	require.Equal(t, 1, result.Failed)
	require.Len(t, result.Errors, 1)
	require.Equal(t, filepath.Join(dir, "bad.md"), result.Errors[0].Path)
	require.ErrorIs(t, result.Errors[0].Err, wantErr)
}

// TestWalkMarkdownFiles_DirectoryReadError verifies that a missing
// directory returns an error and an empty result (caller sees the
// I/O failure rather than a misleading "0 processed").
func TestWalkMarkdownFiles_DirectoryReadError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	result, err := walkMarkdownFiles(missing, func(string) error {
		t.Fatal("ingest must not be called when the directory cannot be read")
		return nil
	})

	require.Error(t, err)
	require.Empty(t, result.Processed)
	require.Empty(t, result.Failed)
	require.Empty(t, result.Errors)
}

// TestWalkMarkdownFiles_EmptyDirectory verifies that an empty (but
// readable) directory yields a clean zero-value result, not an
// error. This is the case the old printf-based path printed
// "Processed: 0, Failed: 0" for, and the new path should still
// report success without pretending there was something to do.
func TestWalkMarkdownFiles_EmptyDirectory(t *testing.T) {
	dir := t.TempDir()

	var calls int
	result, err := walkMarkdownFiles(dir, func(string) error {
		calls++
		return nil
	})

	require.NoError(t, err)
	require.Equal(t, 0, calls)
	require.Equal(t, IngestResult{}, result)
}
