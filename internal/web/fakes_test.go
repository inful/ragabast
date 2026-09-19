package web

import (
	"context"

	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/service"
)

// fakeService is a shared test double that implements the
// serviceAPI interface (defined in server.go). It serves every
// web test: per-test fields are set on the struct and the
// corresponding methods return them. Methods that a test does
// not care about return a harmless zero value.
//
// Fields:
//   - searchResults:    returned by Search
//   - queryAnswer:      returned as the answer in QueryDebugWithOptions
//   - queryDebug:       returned as the debug info; lazy-initialized to
//     an empty QueryDebugInfo if nil
//   - queryErr:         returned as the error from QueryDebugWithOptions,
//     taking precedence over queryAnswer/queryDebug
//   - checkHealthOK:    when false, CheckHealth returns an error
//   - ingestDocument:   returned by IngestDocument (defaults to a
//     stub doc with ID "doc-1" when nil)
//   - ingestErr:        returned by IngestDocument when non-nil
//   - tags/categories:  returned by the catalog lookups; defaults
//     match the legacy fakes so existing tests pass
type fakeService struct {
	searchResults  []models.SearchResult
	queryAnswer    string
	queryDebug     *service.QueryDebugInfo
	queryErr       error
	checkHealthOK  bool
	ingestDocument *models.Document
	ingestErr      error

	tags       []string
	categories []string
}

func (f *fakeService) CheckHealth(context.Context) (bool, error) {
	if !f.checkHealthOK {
		return false, errFakeUnhealthy
	}
	return true, nil
}

func (f *fakeService) IngestDocument(_ context.Context, _ string) (*models.Document, error) {
	if f.ingestErr != nil {
		return nil, f.ingestErr
	}
	if f.ingestDocument != nil {
		return f.ingestDocument, nil
	}
	return &models.Document{ID: "doc-1"}, nil
}

func (f *fakeService) Search(_ context.Context, _ string, _ int, _ map[string]string) ([]models.SearchResult, error) {
	return f.searchResults, nil
}

func (f *fakeService) ListDocuments(context.Context) ([]models.DocumentInfo, error) {
	return []models.DocumentInfo{}, nil
}

func (f *fakeService) DeleteDocument(context.Context, string) error {
	return nil
}

func (f *fakeService) SuggestFrontmatter(context.Context, string, map[string]any, []string, []string) (service.FrontmatterSuggestion, error) {
	return service.FrontmatterSuggestion{}, nil
}

func (f *fakeService) QueryDebugWithOptions(_ context.Context, _ string, _ int, _ service.LLMOptions) (string, *service.QueryDebugInfo, error) {
	if f.queryErr != nil {
		return "", nil, f.queryErr
	}
	if f.queryDebug == nil {
		f.queryDebug = &service.QueryDebugInfo{Results: []models.SearchResult{}}
	}
	return f.queryAnswer, f.queryDebug, nil
}

func (f *fakeService) GetNormalizedTags(context.Context) ([]string, error) {
	if f.tags != nil {
		return f.tags, nil
	}
	return []string{"go", "rag", "api"}, nil
}

func (f *fakeService) GetNormalizedCategories(context.Context) ([]string, error) {
	if f.categories != nil {
		return f.categories, nil
	}
	return []string{"Guides", "Reference", "Tutorials"}, nil
}

func (f *fakeService) GetTagsAndCategories(ctx context.Context) ([]string, []string, error) {
	tags, err := f.GetNormalizedTags(ctx)
	if err != nil {
		return nil, nil, err
	}
	categories, err := f.GetNormalizedCategories(ctx)
	if err != nil {
		return nil, nil, err
	}
	return tags, categories, nil
}

// errFakeUnhealthy is the sentinel returned by CheckHealth when
// the test wants the unhealthy path. Kept private — tests set
// checkHealthOK=false to trigger it.
var errFakeUnhealthy = fakeError("service unhealthy")

type fakeError string

func (e fakeError) Error() string { return string(e) }
