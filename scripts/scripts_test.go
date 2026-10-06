// scripts/scripts_test.go — Pin the structure of the visual
// regression tooling so a future contributor who breaks the
// scripts gets a red test rather than a silent failure.
//
// Why this exists: the screenshot/diff scripts and the
// screenshots/ directory are first-class infrastructure for
// layout verification. A contributor who deletes one of the
// scripts, moves the screenshots/ dir, or breaks the npm
// dependency list shouldn't find out from a missing screenshot
// during a layout bug report — they should find out from a
// fast unit test in the regular `go test ./...` run.
//
// The test runs in <1s: it's just file existence + regex
// matching, no browser, no Node, no I/O beyond stat().

package scripts

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// repoRoot finds the repo root by walking up from this test
// file's directory until it finds go.mod. scripts_test.go
// lives at scripts/scripts_test.go, so two `.dir` is enough
// (scripts -> repo root), but the loop is defensive against
// future moves.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find go.mod from %s", dir)
		}
		dir = parent
	}
}

func TestScripts_AllPresent(t *testing.T) {
	root := repoRoot(t)
	want := []string{
		"scripts/screenshot.mjs",
		"scripts/screenshot-diff.mjs",
		"scripts/screenshot.sh",
		"scripts/screenshot-diff.sh",
	}
	for _, p := range want {
		full := filepath.Join(root, p)
		if _, err := os.Stat(full); err != nil {
			t.Errorf("missing: %s (%v)", p, err)
		}
	}
}

func TestScripts_ShellsAreExecutable(t *testing.T) {
	root := repoRoot(t)
	for _, p := range []string{
		"scripts/screenshot.sh",
		"scripts/screenshot-diff.sh",
	} {
		info, err := os.Stat(filepath.Join(root, p))
		if err != nil {
			t.Errorf("stat %s: %v", p, err)
			continue
		}
		// Check the owner-execute bit. Mode() returns the
		// permission bits; we mask to 0100 (the user-exec
		// bit) and assert it's set.
		if info.Mode()&0o100 == 0 {
			t.Errorf("%s is not executable (mode %o)", p, info.Mode())
		}
	}
}

func TestMakefile_RegistersScreenshotTargets(t *testing.T) {
	root := repoRoot(t)
	makefile, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	body := string(makefile)
	// The screenshot / screenshot-diff / screenshot-baseline
	// targets must be in the .PHONY line and as standalone
	// target definitions. Regex: line starting with the
	// target name and a colon.
	for _, target := range []string{
		"screenshot:",
		"screenshot-diff:",
		"screenshot-baseline:",
	} {
		if !regexp.MustCompile("(?m)^" + regexp.QuoteMeta(target)).MatchString(body) {
			t.Errorf("Makefile missing target definition for %q", target)
		}
	}
	// And the .PHONY line must include them.
	phonyLine := regexp.MustCompile(`(?m)^\.PHONY:\s+(.+)$`).FindStringSubmatch(body)
	if phonyLine == nil {
		t.Fatalf("Makefile has no .PHONY line")
	}
	phonyTargets := strings.Fields(phonyLine[1])
	for _, target := range []string{
		"screenshot",
		"screenshot-diff",
		"screenshot-baseline",
	} {
		found := slices.Contains(phonyTargets, target)
		if !found {
			t.Errorf(".PHONY line does not list %q", target)
		}
	}
}

func TestScreenshotsDir_Structure(t *testing.T) {
	root := repoRoot(t)
	// screenshots/README.md must exist (it documents the
	// directory layout and workflow). The baseline/,
	// actual/, and diff/ subdirs are created on first use
	// (by make screenshot / make screenshot-baseline) so
	// they don't need to exist in a fresh checkout.
	if _, err := os.Stat(filepath.Join(root, "screenshots/README.md")); err != nil {
		t.Errorf("missing: screenshots/README.md (%v)", err)
	}
	// The README must mention both the baseline/ and
	// actual/ directories so a new contributor who finds
	// the dir can discover the workflow without having to
	// ask.
	readme, err := os.ReadFile(filepath.Join(root, "screenshots/README.md"))
	if err != nil {
		t.Fatalf("read screenshots/README.md: %v", err)
	}
	body := string(readme)
	for _, must := range []string{"baseline", "actual", "screenshot-baseline"} {
		if !strings.Contains(body, must) {
			t.Errorf("screenshots/README.md does not mention %q", must)
		}
	}
}

func TestPackageJson_DeclaresScreenshotDeps(t *testing.T) {
	root := repoRoot(t)
	pkg, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		t.Fatalf("read package.json: %v", err)
	}
	body := string(pkg)
	for _, dep := range []string{
		"puppeteer-core",
		"pixelmatch",
		"pngjs",
	} {
		if !strings.Contains(body, `"`+dep+`"`) {
			t.Errorf("package.json does not declare %q as a dependency", dep)
		}
	}
}
