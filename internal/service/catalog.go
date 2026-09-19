package service

import (
	"context"
	"sort"
	"strings"

	"github.com/ragabast/internal/models"
)

// GetNormalizedTags retrieves all unique tags from the vector database, normalized to lowercase.
func (s *Service) GetNormalizedTags(ctx context.Context) ([]string, error) {
	docs, err := s.vectorOps.GetUniqueDocuments(ctx)
	if err != nil {
		return nil, err
	}

	return collectUnique(docs, func(d *models.DocumentInfo) []string { return d.Tags }, strings.ToLower), nil
}

// GetNormalizedCategories retrieves all unique categories from the vector database.
// Categories are kept as-is (preserving capitalization) but trimmed of whitespace.
func (s *Service) GetNormalizedCategories(ctx context.Context) ([]string, error) {
	docs, err := s.vectorOps.GetUniqueDocuments(ctx)
	if err != nil {
		return nil, err
	}

	return collectUnique(docs, func(d *models.DocumentInfo) []string { return d.Categories }, strings.TrimSpace), nil
}

// GetTagsAndCategories retrieves both normalized tags and categories.
func (s *Service) GetTagsAndCategories(ctx context.Context) (tags []string, categories []string, err error) {
	tags, err = s.GetNormalizedTags(ctx)
	if err != nil {
		return nil, nil, err
	}

	categories, err = s.GetNormalizedCategories(ctx)
	if err != nil {
		return nil, nil, err
	}

	return tags, categories, nil
}

// collectUnique folds the values pulled from each document by
// field through normalize, drops empties, dedupes, and returns the
// sorted result. Shared between GetNormalizedTags (lowercase +
// trim) and GetNormalizedCategories (trim only) so the
// "extract → normalize → dedupe → sort" pipeline isn't repeated.
func collectUnique(docs []models.DocumentInfo, field func(*models.DocumentInfo) []string, normalize func(string) string) []string {
	set := make(map[string]struct{})
	for i := range docs {
		for _, v := range field(&docs[i]) {
			n := normalize(v)
			if n == "" {
				continue
			}
			set[n] = struct{}{}
		}
	}

	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
