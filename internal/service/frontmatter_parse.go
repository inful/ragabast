package service

import (
	"encoding/json"
	"errors"
	"log"
	"regexp"
	"strings"
)

// parseFrontmatterSuggestionJSON parses a raw LLM response into a
// FrontmatterSuggestion. It tries, in order:
//  1. extract the outermost {...} and parse with encoding/json
//  2. apply mild repairs (single→double quotes, quoted bare
//     keys, trailing commas) and try again
//  3. apply aggressive repairs (strip Go slice syntax, drop
//     non-JSON-looking lines) and try again
//  4. fall back to constructing JSON from "key: value" lines in
//     case the LLM responded in plain text
//
// When all four fail the response is logged at debug level and
// an error is returned so the caller can surface a 500 to the
// user without leaking the LLM's output.
func parseFrontmatterSuggestionJSON(raw string) (FrontmatterSuggestion, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return FrontmatterSuggestion{}, errors.New("empty response")
	}

	s = stripJSONCodeFence(s)

	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')

	if start != -1 && end != -1 && end > start {
		jsonStr := s[start : end+1]

		var out FrontmatterSuggestion
		if err := json.Unmarshal([]byte(jsonStr), &out); err == nil {
			return out, nil
		}

		repaired := repairJSONObjectLike(jsonStr)
		if err := json.Unmarshal([]byte(repaired), &out); err == nil {
			return out, nil
		}

		aggressive := aggressiveRepairJSON(jsonStr)
		if err := json.Unmarshal([]byte(aggressive), &out); err == nil {
			return out, nil
		}
	}

	constructed := constructJSONFromText(s)
	if constructed != "" {
		var out FrontmatterSuggestion
		if err := json.Unmarshal([]byte(constructed), &out); err == nil {
			return out, nil
		}
	}

	log.Printf("FRONTMATTER_SUGGESTION_PARSE_ERROR: Failed to parse LLM response as JSON")
	log.Printf("FRONTMATTER_SUGGESTION_RAW: %q", raw)
	log.Printf("FRONTMATTER_SUGGESTION_PROCESSED: %q", s)
	log.Printf("FRONTMATTER_SUGGESTION_CONSTRUCTED: %q", constructed)

	return FrontmatterSuggestion{}, errors.New("failed to extract valid JSON from LLM response")
}

// stripJSONCodeFence unwraps a ```json ... ``` (or ``` ... ```)
// markdown fence around the JSON payload, if present. Returns the
// input unchanged when no fence is detected.
func stripJSONCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, "```") {
		return s
	}
	parts := strings.Split(s, "```")

	// Handle edge case where response is just "```" or has no content between fences
	if len(parts) < 2 {
		return s
	}

	var fenced string
	for i := 1; i < len(parts); i++ {
		fenced = strings.TrimSpace(parts[i])
		if fenced != "" {
			break
		}
	}

	if fenced == "" {
		return s
	}

	if idx := strings.Index(fenced, "\n"); idx > 0 {
		first := strings.TrimSpace(fenced[:idx])
		if first == "json" {
			return strings.TrimSpace(fenced[idx+1:])
		}
	}

	return fenced
}

var (
	jsonBareKeyRe   = regexp.MustCompile(`([\{,]\s*)([A-Za-z_][A-Za-z0-9_]*)(\s*:)`)
	jsonTrailingCom = regexp.MustCompile(`,\s*([\}\]])`)
	jsonSingleQuote = regexp.MustCompile(`'([^']*)'`)
)

// repairJSONObjectLike applies mild JSON repairs common in LLM
// outputs: single→double quotes, unquoted keys, trailing commas
// before closing braces or brackets. Does not handle nested
// structural problems; aggressiveRepairJSON covers those.
func repairJSONObjectLike(s string) string {
	out := jsonSingleQuote.ReplaceAllString(s, `"$1"`)
	out = jsonBareKeyRe.ReplaceAllString(out, `$1"$2"$3`)
	out = jsonTrailingCom.ReplaceAllString(out, `$1`)
	out = strings.ReplaceAll(out, `: true`, `: true`)
	out = strings.ReplaceAll(out, `: false`, `: false`)
	out = strings.ReplaceAll(out, `: null`, `: null`)
	return out
}

// aggressiveRepairJSON applies structural repairs for the more
// creative ways an LLM can break JSON: Go slice syntax, stray
// prose lines, double-double-quoted strings.
func aggressiveRepairJSON(s string) string {
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start != -1 && end != -1 && end > start {
		s = s[start : end+1]
	}

	s = repairJSONObjectLike(s)

	goSliceRe := regexp.MustCompile(`\[\]string\{([^}]*)\}`)
	s = goSliceRe.ReplaceAllString(s, `[$1]`)

	s = strings.ReplaceAll(s, `""`, `"`)

	lines := strings.Split(s, "\n")
	var jsonLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, `"`) ||
			strings.Contains(trimmed, `:`) ||
			strings.Contains(trimmed, `[`) ||
			strings.Contains(trimmed, `]`) ||
			strings.Contains(trimmed, `{`) ||
			strings.Contains(trimmed, `}`) ||
			trimmed == "true" || trimmed == "false" || trimmed == "null" {
			jsonLines = append(jsonLines, line)
		}
	}
	return strings.Join(jsonLines, "\n")
}

// constructJSONFromText builds a JSON object from "key: value"
// lines. Used as the last-resort fallback when the LLM responds
// in plain text rather than JSON. Only returns a non-empty string
// when at least one of the four expected fields is found.
func constructJSONFromText(text string) string {
	description := ""
	categories := []string{}
	tags := []string{}
	customTags := []string{}

	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if key, val, ok := splitKeyColon(line); ok {
			switch strings.ToLower(key) {
			case "description":
				description = strings.TrimSpace(val)
				continue
			case "categories":
				categories = parseArrayValue(val)
				continue
			case "tags":
				tags = parseArrayValue(val)
				continue
			}
		}
		if key, val, ok := splitKeyColon(line); ok {
			switch strings.ToLower(strings.ReplaceAll(key, " ", "")) {
			case "custom_tags", "customtags":
				customTags = parseArrayValue(val)
				continue
			}
		}
	}

	if description == "" && len(categories) == 0 && len(tags) == 0 && len(customTags) == 0 {
		return ""
	}

	jsonBytes, err := json.Marshal(map[string]any{
		"description": description,
		"categories":  categories,
		"tags":        tags,
		"custom_tags": customTags,
	})
	if err != nil {
		return ""
	}
	return string(jsonBytes)
}

// splitKeyColon splits "key: value" (or "key:value") into its
// parts. Returns ok=false when no colon is present so callers
// can skip non-keyed lines. Whitespace around key and value is
// preserved; callers trim as needed.
func splitKeyColon(line string) (key, val string, ok bool) {
	before, after, ok := strings.Cut(line, ":")
	if !ok {
		return "", "", false
	}
	return before, after, true
}

// parseArrayValue extracts string values from either [a, b, c]
// or comma-separated forms, dropping quotes.
func parseArrayValue(val string) []string {
	val = strings.TrimSpace(val)
	if val == "" {
		return []string{}
	}

	if strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]") {
		val = strings.TrimPrefix(val, "[")
		val = strings.TrimSuffix(val, "]")
		parts := strings.Split(val, ",")
		result := []string{}
		for _, part := range parts {
			trimmed := strings.Trim(strings.TrimSpace(part), `"'`)
			if trimmed != "" {
				result = append(result, trimmed)
			}
		}
		return result
	}

	parts := strings.Split(val, ",")
	result := []string{}
	for _, part := range parts {
		trimmed := strings.Trim(strings.TrimSpace(part), `"'`)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
