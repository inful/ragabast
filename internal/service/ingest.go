package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ragabast/internal/models"
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

// IngestFile processes a single docbuilder file.
func (s *Service) IngestFile(ctx context.Context, filePath string) error {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", filePath, err)
	}

	doc, err := s.parser.ParseDocument(content, filePath)
	if err != nil {
		return fmt.Errorf("failed to parse document %s: %w", filePath, err)
	}

	if err := chunkAndIngest(ctx, s.chunker, s.vectorOps, doc); err != nil {
		return fmt.Errorf("ingest %s: %w", filePath, err)
	}
	return nil
}

// IngestDirectory processes all docbuilder files in a directory.
//
// The returned IngestResult counts successes and failures and lists
// per-file errors. Presentation (logging, printing, surfacing to a
// web client) is the caller's responsibility; this method does not
// write to stdout or stderr.
func (s *Service) IngestDirectory(ctx context.Context, dirPath string) (IngestResult, error) {
	return walkMarkdownFiles(dirPath, func(path string) error {
		return s.IngestFile(ctx, path)
	})
}

// IngestDocument processes a docbuilder document from raw content.
func (s *Service) IngestDocument(ctx context.Context, content string) (*models.Document, error) {
	doc, err := s.parser.ParseDocument([]byte(content), "web_upload")
	if err != nil {
		return nil, fmt.Errorf("failed to parse document: %w", err)
	}

	if err := chunkAndIngest(ctx, s.chunker, s.vectorOps, doc); err != nil {
		return nil, err
	}
	// Invalidate the query cache (issue #13): any new
	// chunk could shift result rankings. Conservative
	// whole-cache clear — operators see a brief hit-rate
	// dip during heavy ingest, which is the right
	// tradeoff vs serving stale rankings.
	s.cache.Clear()
	return doc, nil
}
