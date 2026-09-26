package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/describe"
	"github.com/egladman/magus/libs/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

// repoRoot is this repository's root, relative to the package under test.
const repoRoot = "../.."

// repoFS loads paths from the real tree, so a clean case is the file as it ships.
func repoFS(t *testing.T, paths ...string) fstest.MapFS {
	t.Helper()
	fsys := fstest.MapFS{}
	for _, p := range paths {
		body, err := os.ReadFile(filepath.Join(repoRoot, p))
		require.NoError(t, err)
		fsys[p] = &fstest.MapFile{Data: body}
	}
	return fsys
}

// seed rewrites old to replacement in one file of fsys, failing when old is
// absent so a seeded violation cannot silently seed nothing.
func seed(t *testing.T, fsys fstest.MapFS, path, old, replacement string) {
	t.Helper()
	body := string(fsys[path].Data)
	require.Contains(t, body, old, "seed anchor missing from %s", path)
	fsys[path] = &fstest.MapFile{Data: []byte(strings.Replace(body, old, replacement, 1))}
}

func problems(findings []finding) []string {
	out := make([]string, 0, len(findings))
	for _, f := range findings {
		out = append(out, f.problem)
	}
	return out
}

func TestFindingNamesPositionProblemAndFix(t *testing.T) {
	f := finding{path: "a.lock", line: 3, problem: "b follows c", fix: "Move it.\nSecond line."}
	assert.Equal(t, "a.lock:3: b follows c\n    Move it.\n    Second line.", f.String())
	assert.Equal(t, "a.lock: gone", finding{path: "a.lock", problem: "gone"}.String())
}

func TestRunReportsEveryFindingUnderItsRule(t *testing.T) {
	var out bytes.Buffer
	n := run(fstest.MapFS{"x.lock": {Data: []byte("b\na\n")}}, &out)
	assert.Positive(t, n)
	assert.Contains(t, out.String(),
		"x.lock:2: lockfile entry out of byte order (or duplicated) within its block: a follows b [lockfiles-are-sorted]\n    Move")
}

// A rule whose subject is missing reports that rather than passing. Lockfiles
// are the exception: a tree with none has nothing to order.
func TestRulesReportAMissingSubject(t *testing.T) {
	for _, r := range rules {
		if r.name == "lockfiles-are-sorted" {
			continue
		}
		assert.NotEmpty(t, r.check(fstest.MapFS{}), r.name)
	}
}

func TestFullTwinSuffixMatchesTheInstaller(t *testing.T) {
	assert.Equal(t, agent.FullTwinName("x"), "x"+fullTwinSuffix)
}

// lintFilesFootprint is the root magusfile's declared footprint for this tool's target.
func lintFilesFootprint(t *testing.T) []string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot, rootMagusfile))
	require.NoError(t, err)
	for _, n := range describe.Extract(string(body)) {
		if n.Name != "lint-files" {
			continue
		}
		var globs []string
		for _, ref := range n.ReadsFiles {
			globs = append(globs, ref.Glob)
		}
		return globs
	}
	require.Fail(t, "the root magusfile declares no lint-files target")
	return nil
}

func declared(globs []string, path string) bool {
	for _, g := range globs {
		if ok, _ := doublestar.Match(g, path); ok {
			return true
		}
	}
	return false
}

// The target keys on the Go this tool compiles from the tree. A new import that
// the footprint misses would let an edit to it replay a verdict computed by
// different code.
func TestLintFilesKeysOnEveryPackageTheToolCompiles(t *testing.T) {
	globs := lintFilesFootprint(t)
	root, err := filepath.Abs(repoRoot)
	require.NoError(t, err)
	out, err := exec.CommandContext(t.Context(), "go", "list", "-deps", "-f", "{{if .Module}}{{.Dir}}{{end}}", ".").Output()
	require.NoError(t, err)
	checked := 0
	for dir := range strings.Lines(string(out)) {
		rel, err := filepath.Rel(root, strings.TrimSpace(dir))
		if err != nil || strings.HasPrefix(rel, "..") || strings.Contains(rel, "/pkg/mod/") {
			continue
		}
		checked++
		assert.True(t, declared(globs, filepath.ToSlash(rel)+"/x.go"),
			"lint-files compiles %s but its ctx.readsFiles names no glob covering it", rel)
	}
	assert.Positive(t, checked)
}

func TestLintFilesKeysOnEveryFileTheRulesRead(t *testing.T) {
	globs := lintFilesFootprint(t)
	for _, p := range []string{
		setupMagusAction, queueApplyFlow, ".github/workflows/ci.yaml", rootMagusfile,
		"console/magusfile.buzz", embeddedSkillDir + "/magus-run/SKILL.md", landingMarkup, siteStyles,
		shippedHookConfigs["claude-code"], shippedHookConfigs["codex"], "docs/active.urls.lock", "benchmarks/versions.lock",
	} {
		assert.True(t, declared(globs, p), "a rule reads %s but lint-files does not declare it", p)
	}
}
