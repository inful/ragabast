package web

import (
	"context"

	"github.com/ragabast/internal/models"
	"github.com/ragabast/internal/service"
)

// serviceAPI is the slice of *service.Service that the web layer
// depends on. Lives in its own file so the contract between web
// and service is visible without scrolling through server.go's
// lifecycle code. Tests substitute a fake; production gets the
// real *service.Service via web.NewServer.
type serviceAPI interface {
	CheckHealth(ctx context.Context) (bool, error)
	IngestDocument(ctx context.Context, content string) (*models.Document, error)
	Search(ctx context.Context, query string, limit int, filters service.SearchFilters) ([]models.SearchResult, error)
	HybridSearch(ctx context.Context, query string, limit int, filters service.SearchFilters, mode service.SearchMode) ([]models.SearchResult, error)
	ListDocuments(ctx context.Context) ([]models.DocumentInfo, error)
	DeleteDocument(ctx context.Context, documentID string) error
	SuggestFrontmatter(ctx context.Context, content string, existing map[string]any, allowedCategories []string, allowedTags []string) (service.FrontmatterSuggestion, error)
	QueryDebugWithOptions(ctx context.Context, query string, limit int, opts service.LLMOptions) (string, *service.QueryDebugInfo, error)
	GetNormalizedTags(ctx context.Context) ([]string, error)
	GetNormalizedCategories(ctx context.Context) ([]string, error)
	GetTagsAndCategories(ctx context.Context) (tags []string, categories []string, err error)
}

// Compile-time assertion that *service.Service satisfies serviceAPI.
// Without this, adding a method to serviceAPI that *service.Service
// does not implement would compile fine and only blow up at runtime
// when web.NewServer is handed the wrong type. With this, drift is
// caught at `go build` time. The same check applied via reflection
// (e.g. in a test) would miss the next divergence; the compiler
// check survives indefinitely.
var _ serviceAPI = (*service.Service)(nil)
