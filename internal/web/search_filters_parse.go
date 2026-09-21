package web

import (
	"fmt"
	"time"

	"github.com/ragabast/internal/service"
)

// parseSearchFilters parses the four RFC3339 date fields
// from the request body into *time.Time pointers on the
// service-layer SearchFilters. Invalid input returns an
// error so the huma layer can surface a 400 with the
// offending field name.
//
// Why string-typed request fields: huma's schema layer
// works best with primitives, and RFC3339 is a
// well-defined wire format. The cost is one parse call
// per field; operators hitting the endpoint with a script
// get the validation message back as a 400.
//
// RFC3339-Nano timestamps (\"2024-06-15T12:00:00.123Z\")
// are accepted in addition to plain RFC3339 — Go's
// time.Parse with the \"RFC3339\" layout accepts both.
func parseSearchFilters(base service.SearchFilters, createdAfter, createdBefore, updatedAfter, updatedBefore *string) (service.SearchFilters, error) {
	f := base
	if createdAfter != nil && *createdAfter != "" {
		t, err := time.Parse(time.RFC3339, *createdAfter)
		if err != nil {
			return f, fmt.Errorf("created_after: %w", err)
		}
		f.CreatedAfter = &t
	}
	if createdBefore != nil && *createdBefore != "" {
		t, err := time.Parse(time.RFC3339, *createdBefore)
		if err != nil {
			return f, fmt.Errorf("created_before: %w", err)
		}
		f.CreatedBefore = &t
	}
	if updatedAfter != nil && *updatedAfter != "" {
		t, err := time.Parse(time.RFC3339, *updatedAfter)
		if err != nil {
			return f, fmt.Errorf("updated_after: %w", err)
		}
		f.UpdatedAfter = &t
	}
	if updatedBefore != nil && *updatedBefore != "" {
		t, err := time.Parse(time.RFC3339, *updatedBefore)
		if err != nil {
			return f, fmt.Errorf("updated_before: %w", err)
		}
		f.UpdatedBefore = &t
	}
	return f, nil
}
