package service

import (
	"fmt"
	"strings"

	"github.com/ragabast/internal/models"
)

func buildQueryContext(results []models.SearchResult) string {
	var contextBuilder strings.Builder
	for i, result := range results {
		contextBuilder.WriteString(fmt.Sprintf("Result %d (from %s):\n%s\n\n", i+1, result.DocumentTitle, result.Content))
	}
	return contextBuilder.String()
}

func buildQueryPrompt(query string, context string) string {
	return fmt.Sprintf("Based on the following context, answer the question: %s\n\nContext:\n%s", query, context)
}
