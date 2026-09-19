package web

import (
	"context"
	"log"
	"maps"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"gopkg.in/yaml.v3"
)

type frontmatterSuggestRequestBody struct {
	Content           string   `doc:"Document content (may include YAML frontmatter)." json:"content"`
	AllowedCategories []string `doc:"Allowed categories list (suggestions must come from here)." json:"allowed_categories"`
	AllowedTags       []string `doc:"Preferred tags list (suggestions should prefer these)." json:"allowed_tags"`
}

type frontmatterSuggestResponseBody struct {
	Frontmatter map[string]any `json:"frontmatter"`
	Applied     map[string]any `json:"applied"`
}

// splitDocbuilderFrontmatter pulls the YAML frontmatter (between
// the outer --- markers) out of a docbuilder document, returning
// the trimmed frontmatter bytes, the trimmed markdown body, and a
// bool that says whether the document actually had a frontmatter
// block. Returns ok=false (and the original content as markdown)
// when the document is not wrapped in --- markers.
//
// Kept here rather than in the parser package because it is only
// used to seed the frontmatter-suggest endpoint. The parser has
// its own extractFrontmatter for the ingest path; we deliberately
// don't share because they have slightly different tolerance
// (this one trims more aggressively, the parser one validates).
func splitDocbuilderFrontmatter(raw string) (frontmatterYAML []byte, markdown string, ok bool) {
	content := strings.TrimSpace(raw)
	if !strings.HasPrefix(content, "---\n") {
		return nil, content, false
	}
	rest := content[len("---\n"):]
	before, after, ok0 := strings.Cut(rest, "\n---\n")
	if !ok0 {
		return nil, content, false
	}
	fm := strings.TrimSpace(before)
	md := strings.TrimSpace(after)
	return []byte(fm), md, true
}

// registerFrontmatterOperation wires POST /api/frontmatter/suggest.
func registerFrontmatterOperation(api huma.API, svc serviceAPI) {
	huma.Register(api, huma.Operation{
		OperationID: "frontmatter-suggest",
		Method:      http.MethodPost,
		Path:        "/api/frontmatter/suggest",
		Summary:     "Suggest frontmatter fields",
		Description: "Uses the LLM to propose description, tags and categories from the document content. This endpoint does NOT retrieve vector DB context.",
	}, func(ctx context.Context, input *struct{ Body frontmatterSuggestRequestBody }) (*struct {
		Body frontmatterSuggestResponseBody
	}, error,
	) {
		content := strings.TrimSpace(input.Body.Content)
		if content == "" {
			return nil, huma.Error400BadRequest("content is required")
		}
		if len(input.Body.AllowedCategories) == 0 {
			return nil, huma.Error400BadRequest("allowed_categories is required")
		}
		if len(input.Body.AllowedTags) == 0 {
			return nil, huma.Error400BadRequest("allowed_tags is required")
		}

		fmBytes, markdown, hasFM := splitDocbuilderFrontmatter(content)
		existing := map[string]any{}
		if hasFM && len(fmBytes) > 0 {
			if err := yaml.Unmarshal(fmBytes, &existing); err != nil {
				return nil, huma.Error400BadRequest("invalid YAML frontmatter")
			}
		}

		mergedAllowedCategories := uniqueNonEmptyStrings(input.Body.AllowedCategories)
		mergedAllowedTags := uniqueNonEmptyStrings(input.Body.AllowedTags)

		if raw, ok := existing["categories"].([]any); ok {
			existingCats := normalizeStringSlice(raw)
			mergedAllowedCategories = uniqueAppend(mergedAllowedCategories, existingCats)
		}

		if raw, ok := existing["tags"].([]any); ok {
			existingTags := normalizeStringSlice(raw)
			mergedAllowedTags = uniqueAppend(mergedAllowedTags, existingTags)
		}

		sug, err := svc.SuggestFrontmatter(ctx, markdown, existing, mergedAllowedCategories, mergedAllowedTags)
		if err != nil {
			preview := input.Body.Content
			if len(preview) > 80 {
				preview = preview[:80]
			}
			log.Printf("huma: frontmatter-suggest %q: %v", preview, err)
			return nil, huma.Error500InternalServerError("frontmatter suggestion failed")
		}

		applied := map[string]any{}
		merged := make(map[string]any, len(existing)+3)
		maps.Copy(merged, existing)

		// Description: only set if empty/missing.
		if cur, ok := merged["description"].(string); ok && strings.TrimSpace(cur) != "" {
			applied["description_set"] = false
		} else if strings.TrimSpace(sug.Description) != "" {
			merged["description"] = strings.TrimSpace(sug.Description)
			applied["description_set"] = true
		} else {
			applied["description_set"] = false
		}

		// Categories: keep existing, add allowed suggestions.
		existingCats := []string{}
		if raw, ok := merged["categories"].([]any); ok {
			existingCats = normalizeStringSlice(raw)
		}
		addedCats := filterAllowed(sug.Categories, input.Body.AllowedCategories)
		finalCats := canonicalizeCategories(uniqueAppend(existingCats, addedCats), input.Body.AllowedCategories)
		if len(finalCats) > 0 {
			merged["categories"] = finalCats
		}
		applied["categories_added"] = addedCats

		// Tags: keep existing, add allowed tags + custom tags.
		existingTags := []string{}
		if raw, ok := merged["tags"].([]any); ok {
			existingTags = normalizeStringSlice(raw)
		}
		addedAllowedTags, customFromTags := splitAllowedAndCustomTags(sug.Tags, input.Body.AllowedTags)
		customTags := uniqueAppend(customFromTags, uniqueNonEmptyStrings(sug.CustomTags))
		finalTags := normalizeTagsLower(uniqueAppend(existingTags, uniqueAppend(addedAllowedTags, customTags)))
		if len(finalTags) > 0 {
			merged["tags"] = finalTags
		}
		applied["tags_added"] = addedAllowedTags
		applied["custom_tags_added"] = normalizeTagsLower(customTags)

		return &struct {
			Body frontmatterSuggestResponseBody
		}{Body: frontmatterSuggestResponseBody{Frontmatter: merged, Applied: applied}}, nil
	})
}
