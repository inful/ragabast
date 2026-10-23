package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/ragabast/internal/service"
)

// TestParseSearchModeFlag pins the CLI string -> service.SearchMode
// translation. This function had no coverage at all (the cmd
// package sat at 34.5%), and it is the CLI half of a contract
// the JSON wire parser also implements — a divergence between
// the two surfaces would show up as the CLI and the API
// disagreeing about what "semantic" means.
func TestParseSearchModeFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want service.SearchMode
	}{
		{"semantic", service.ModeSemantic},
		{"keyword", service.ModeKeyword},
		{"hybrid", service.ModeHybrid},
		// Empty means "not specified" -> the hybrid default.
		{"", service.ModeHybrid},
		// Case and surrounding whitespace are normalized.
		{"SEMANTIC", service.ModeSemantic},
		{"  Keyword  ", service.ModeKeyword},
		{" Hybrid ", service.ModeHybrid},
		// An unknown mode falls back to hybrid rather than
		// erroring — the CLI has no way to report a 422.
		{"bogus", service.ModeHybrid},
	}

	for _, tc := range tests {
		assert.Equal(t, tc.want, parseSearchModeFlag(tc.in),
			"parseSearchModeFlag(%q)", tc.in)
	}
}
