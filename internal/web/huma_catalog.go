package web

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

type healthResponseBody struct {
	Status string `json:"status"`
}

type tagsResponseBody struct {
	Tags []string `json:"tags"`
}

type categoriesResponseBody struct {
	Categories []string `json:"categories"`
}

type tagsAndCategoriesResponseBody struct {
	Tags       []string `json:"tags"`
	Categories []string `json:"categories"`
}

// registerCatalogOperations wires the small read-only catalog
// endpoints: health check and the three tag/category lookups.
func registerCatalogOperations(api huma.API, svc serviceAPI) {
	huma.Register(api, huma.Operation{
		OperationID: "health",
		Method:      http.MethodGet,
		Path:        "/api/health",
		Summary:     "Health check",
	}, func(ctx context.Context, input *struct{}) (*struct{ Body healthResponseBody }, error) {
		_, err := svc.CheckHealth(ctx)
		if err != nil {
			return nil, huma.Error503ServiceUnavailable("Service unhealthy")
		}
		return &struct{ Body healthResponseBody }{Body: healthResponseBody{Status: "healthy"}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-tags",
		Method:      http.MethodGet,
		Path:        "/api/tags",
		Summary:     "Get normalized tags",
		Description: "Returns all unique tags from the vector database, normalized to lowercase and sorted.",
	}, func(ctx context.Context, input *struct{}) (*struct{ Body tagsResponseBody }, error) {
		tags, err := svc.GetNormalizedTags(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to retrieve tags")
		}
		return &struct{ Body tagsResponseBody }{Body: tagsResponseBody{Tags: tags}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-categories",
		Method:      http.MethodGet,
		Path:        "/api/categories",
		Summary:     "Get normalized categories",
		Description: "Returns all unique categories from the vector database, trimmed of whitespace and sorted.",
	}, func(ctx context.Context, input *struct{}) (*struct{ Body categoriesResponseBody }, error) {
		categories, err := svc.GetNormalizedCategories(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to retrieve categories")
		}
		return &struct{ Body categoriesResponseBody }{Body: categoriesResponseBody{Categories: categories}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-tags-and-categories",
		Method:      http.MethodGet,
		Path:        "/api/tags-categories",
		Summary:     "Get both tags and categories",
		Description: "Returns all unique tags (lowercase) and categories (preserved case) from the vector database.",
	}, func(ctx context.Context, input *struct{}) (*struct{ Body tagsAndCategoriesResponseBody }, error) {
		tags, categories, err := svc.GetTagsAndCategories(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to retrieve tags and categories")
		}
		return &struct{ Body tagsAndCategoriesResponseBody }{
			Body: tagsAndCategoriesResponseBody{
				Tags:       tags,
				Categories: categories,
			},
		}, nil
	})
}
