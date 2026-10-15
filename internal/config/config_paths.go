package config

import (
	"os"
	"path/filepath"
	"strings"
)

// PathsConfig holds file path configuration.
type PathsConfig struct {
	// DataDir is the base directory for all data.
	DataDir string `env:"DATA_DIR" yaml:"data_dir"`

	// TemplatesDir is an optional override. Empty (the default)
	// tells the web server to use the templates embedded into
	// the binary (see internal/web/templates.go). Set this to
	// point at a directory of *.html files to ship a custom
	// template set without rebuilding.
	TemplatesDir string `env:"TEMPLATES_DIR" yaml:"templates_dir"`
}

// resolvePath expands a leading "~/" or "~" to the user's home
// directory so users can write `--config ~/.ragabast.yml` from
// a shell that does not auto-expand tilde (env-var assignments,
// quoted strings, systemd units, Docker exec).
//
// Only the bare-username form is supported; "~user/path" is left
// literal so os.ReadFile surfaces a clear "no such file" error
// rather than silently mangling the path. Other inputs (absolute
// paths, relative paths, empty strings) pass through unchanged.
//
// os.UserHomeDir() is best-effort: if HOME / USERPROFILE lookup
// fails for any reason, resolvePath returns the input unchanged
// rather than producing a wrong path.
func resolvePath(path string) string {
	if path == "" || path[0] != '~' {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}
