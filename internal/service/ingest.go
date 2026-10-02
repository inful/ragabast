package service

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/parser"
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

	// Hugo "don't publish" preflight (issue #97). Filtered
	// documents skip the chunking path entirely; an error
	// from the helper still surfaces to the caller so a
	// transient vector-store failure isn't mistaken for a
	// silent skip.
	filtered, err := s.applyUnpublishedPreflight(ctx, doc, s.now())
	if err != nil {
		return fmt.Errorf("preflight %s: %w", filePath, err)
	}
	if filtered {
		return nil
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

	// Hugo "don't publish" preflight (issue #97). Returns the
	// parsed doc even when filtered — the caller asked us to
	// process this content; we processed it (by deleting the
	// previously-embedded copy or by ignoring it).
	filtered, err := s.applyUnpublishedPreflight(ctx, doc, s.now())
	if err != nil {
		return nil, err
	}
	if filtered {
		return doc, nil
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

// applyUnpublishedPreflight checks the parsed document against
// Hugo's "don't publish" frontmatter markers (issue #97). When the
// document should not be published under Hugo's defaults:
//
//   - if it was previously embedded, delete it from the embeddings;
//   - if it was never embedded, ignore the request silently.
//
// Returns filtered=true so the caller knows to skip the chunking
// path. The pure detection logic lives in parser.IsUnpublished; this
// helper layers the I/O (existence check + delete) on top.
//
// Errors from the existence check or the delete propagate to the
// caller — a transient vector-store failure is not the same as a
// silent skip, and the operator should see it in the logs.
func (s *Service) applyUnpublishedPreflight(ctx context.Context, doc *models.Document, now time.Time) (filtered bool, err error) {
	if !parser.IsUnpublished(doc, now) {
		return false, nil
	}
	uid := doc.UID
	if uid == "" {
		// Defensive: ParseDocument already validated UID.
		// If we got here with an empty UID something else
		// is wrong — surface it rather than silently skipping.
		return false, fmt.Errorf("preflight called with empty UID on unpublished doc %q", doc.Title)
	}
	_, exists, err := s.GetDocumentFingerprint(ctx, uid)
	if err != nil {
		return false, fmt.Errorf("check existence of unpublished doc %q: %w", uid, err)
	}
	if !exists {
		log.Printf("service: skipping ingest of unpublished doc %q (not previously embedded)", uid)
		return true, nil
	}
	if err := s.DeleteDocument(ctx, uid); err != nil {
		return true, fmt.Errorf("remove unpublished doc %q from embeddings: %w", uid, err)
	}
	log.Printf("service: removed unpublished doc %q from embeddings", uid)
	return true, nil
}
