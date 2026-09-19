package service

import (
	"fmt"
	"os"
	"path/filepath"
)

// IngestResult summarizes a batch ingestion. Callers (CLI, future
// web handlers) own how to present these counts and errors; the
// service layer never logs directly.
type IngestResult struct {
	Processed int
	Failed    int
	Errors    []FileError
}

// FileError pairs a path with the error that stopped its ingest.
// Paths are the original (joined) form, not just basenames, so
// logs and CLI output can disambiguate files with the same name in
// different directories.
type FileError struct {
	Path string
	Err  error
}

// markdownExt is the only file extension Service.IngestDirectory
// ingests. Centralized so it stays in sync with the parser's
// expectations.
const markdownExt = ".md"

// walkMarkdownFiles iterates the immediate children of dirPath,
// invokes ingest for every *.md file, and returns counts plus
// per-file errors. Non-markdown files (and subdirectories) are
// silently skipped. A failure reading the directory itself bubbles
// up as an error with an empty IngestResult so callers can
// distinguish "nothing to do" from "I couldn't look".
//
// This is split out from Service.IngestDirectory so the walk
// policy can be unit-tested without a real vector DB: tests pass
// in an ingest closure that records its arguments.
func walkMarkdownFiles(dirPath string, ingest func(path string) error) (IngestResult, error) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return IngestResult{}, fmt.Errorf("failed to read directory %s: %w", dirPath, err)
	}

	var result IngestResult
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if filepath.Ext(entry.Name()) != markdownExt {
			continue
		}

		filePath := filepath.Join(dirPath, entry.Name())
		if err := ingest(filePath); err != nil {
			result.Errors = append(result.Errors, FileError{Path: filePath, Err: err})
			result.Failed++
			continue
		}
		result.Processed++
	}
	return result, nil
}
