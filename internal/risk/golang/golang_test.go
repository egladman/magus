package golang

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/risk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixtureModule is a module whose import graph every test below reasons about:
//
//	a <- b <- c <- app (main)     e's TEST imports a
//	d runs `go run ../gen`        gen (main) <- genlib
//	skills embeds doc.md          w builds only on windows
var fixtureModule = map[string]string{
	"go.mod":           "module example.com/fx\n\ngo 1.25\n",
	"a/a.go":           "package a\n\n// A is the leaf.\nfunc A() int { return 1 }\n",
	"b/b.go":           "package b\n\nimport \"example.com/fx/a\"\n\nfunc B() int { return a.A() }\n",
	"c/c.go":           "package c\n\nimport \"example.com/fx/b\"\n\nfunc C() int { return b.B() }\n",
	"app/main.go":      "package main\n\nimport \"example.com/fx/c\"\n\nfunc main() { _ = c.C() }\n",
	"d/d.go":           "package d\n\n//go:generate go run ../gen\n\nfunc D() {}\n",
	"e/e.go":           "package e\n\nfunc E() {}\n",
	"e/e_test.go":      "package e\n\nimport (\n\t\"testing\"\n\n\t\"example.com/fx/a\"\n)\n\nfunc TestE(t *testing.T) { _ = a.A() }\n",
	"gen/main.go":      "package main\n\nimport \"example.com/fx/genlib\"\n\nfunc main() { genlib.Run() }\n",
	"genlib/genlib.go": "package genlib\n\nfunc Run() {}\n",
	"skills/skills.go": "package skills\n\nimport _ \"embed\"\n\n//go:embed doc.md\nvar Doc string\n",
	"skills/doc.md":    "# Skill\n",
	"w/w.go":           "package w\n",
	"w/w_windows.go":   "package w\n",
	"a/testdata/x.go":  "package fixture\n",
	"README.md":        "# fx\n",
}

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for p, body := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(body), 0o644))
	}
	return root
}

func with(base, over map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(over))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		out[k] = v
	}
	return out
}

var spellDirectives = []string{"go:", "nolint", "export", "line ", "+build", "sys", "extern"}

// TestPlace pins where each kind of file lands: compiled, test-only and embedded files
// in their package; generator code, files this platform does not build, and Go no
// package compiles unplaced; everything else unmentioned.
func TestPlace(t *testing.T) {
	root := writeTree(t, with(fixtureModule, map[string]string{"z/z.go": "package z\n"}))
	require.NoError(t, os.Remove(filepath.Join(root, "z/z.go")))
	pl, err := New(spellDirectives).Place(context.Background(), root, []string{
		"b/b.go", "e/e_test.go", "skills/doc.md", "genlib/genlib.go", "w/w_windows.go", "a/testdata/x.go", "README.md", "z/z.go",
	})
	require.NoError(t, err)
	assert.Equal(t, map[string]risk.PackageHit{
		"b/b.go":        {Package: "example.com/fx/b", Module: ".", Why: "Go package example.com/fx/b: its tests and every package importing it"},
		"e/e_test.go":   {Package: "example.com/fx/e", Module: ".", TestOnly: true, Why: "test file of Go package example.com/fx/e: only that package's tests compile it"},
		"skills/doc.md": {Package: "example.com/fx/skills", Module: ".", Why: "embedded by Go package example.com/fx/skills (go:embed)"},
	}, pl.Packages)
	assert.Equal(t, map[string]string{
		"genlib/genlib.go": "example.com/fx/genlib is reachable only from go:generate program example.com/fx/gen, so it is generator code",
		"w/w_windows.go":   "excluded by build constraints on this platform, so its tests cannot run here",
		"a/testdata/x.go":  "compiled into no package of module . (testdata, or a directory go does not build)",
		"z/z.go":           "deleted with its whole package, so no package is left to test its former importers against",
	}, pl.Unplaced)
	assert.Equal(t, "go::go-test", pl.Narrows)
}

// TestNarrowTest: a narrowed `go test` keeps its flags and passes on only the packages
// its own patterns name that the closure keeps; with none, or patterns go cannot list,
// it runs as written.
func TestNarrowTest(t *testing.T) {
	root := writeTree(t, fixtureModule)
	keep := []string{"example.com/fx/a", "example.com/fx/b", "example.com/fx/e"}
	narrow := NarrowTest(keep)
	ctx := context.Background()

	tests := []struct {
		name string
		dir  string
		args []string
		want []string
	}{
		{"whole module", root, []string{"test", "-race", "-coverprofile=c.out", "./..."},
			[]string{"test", "-race", "-coverprofile=c.out", "example.com/fx/a", "example.com/fx/b", "example.com/fx/e"}},
		{"value flags keep their values", root, []string{"test", "-run", "TestE", "-count", "1", "./e/...", "./c"},
			[]string{"test", "-run", "TestE", "-count", "1", "example.com/fx/e"}},
		{"import path patterns", root, []string{"test", "example.com/fx/..."},
			[]string{"test", "example.com/fx/a", "example.com/fx/b", "example.com/fx/e"}},
		{"the test binary's args stay last", root, []string{"test", "./a", "-args", "-v", "./c"},
			[]string{"test", "example.com/fx/a", "-args", "-v", "./c"}},
		{"no pattern is the working directory's package", filepath.Join(root, "b"), []string{"test", "-v"},
			[]string{"test", "-v", "example.com/fx/b"}},
		{"nothing kept runs as written", root, []string{"test", "./c", "./app"}, []string{"test", "./c", "./app"}},
		{"an unlistable pattern runs as written", root, []string{"test", "./missing/..."}, []string{"test", "./missing/..."}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, narrow(ctx, tt.dir, nil, tt.args))
		})
	}
}

// TestPlaceDeletedFileKeepsItsPackage: a file deleted from a package that survives is
// placed in it, since its importers are what can break.
func TestPlaceDeletedFileKeepsItsPackage(t *testing.T) {
	root := writeTree(t, with(fixtureModule, map[string]string{"a/extra.go": "package a\n\nfunc X() {}\n"}))
	require.NoError(t, os.Remove(filepath.Join(root, "a/extra.go")))
	pl, err := New(spellDirectives).Place(context.Background(), root, []string{"a/extra.go"})
	require.NoError(t, err)
	assert.Equal(t, "example.com/fx/a", pl.Packages["a/extra.go"].Package)
}

// TestPlaceClosureMatchesGoList: the closure is go's own answer, package by package: P
// is in the closure of a exactly when `go list -deps -test P` names a.
func TestPlaceClosureMatchesGoList(t *testing.T) {
	root := writeTree(t, fixtureModule)
	pl, err := New(spellDirectives).Place(context.Background(), root, []string{"a/a.go"})
	require.NoError(t, err)

	list := exec.Command("go", "list", "./...")
	list.Dir = root
	listed, err := list.Output()
	require.NoError(t, err)
	var want []string
	for pkg := range strings.FieldsSeq(string(listed)) {
		cmd := exec.Command("go", "list", "-deps", "-test", pkg)
		cmd.Dir = root
		deps, err := cmd.Output()
		require.NoError(t, err)
		for d := range strings.FieldsSeq(string(deps)) {
			if d == "example.com/fx/a" || strings.HasPrefix(d, "example.com/fx/a [") {
				want = append(want, pkg)
				break
			}
		}
	}
	slices.Sort(want)
	want = slices.Compact(want)

	assert.Equal(t, want, pl.Closure([]string{"example.com/fx/a"}, nil))
	assert.Equal(t, []string{"example.com/fx/a", "example.com/fx/app", "example.com/fx/b", "example.com/fx/c", "example.com/fx/e"}, want)
	assert.Equal(t, []string{"example.com/fx/e"}, pl.Closure(nil, []string{"example.com/fx/e"}), "a test file reaches only its own package")
	assert.Empty(t, pl.Closure([]string{"example.com/other/z"}, nil), "a package of no loaded module reaches nothing")
}

// TestPlacePoisonedModule: a load error anywhere in a module makes every path in it
// unplaced, since a package that failed to load may import the change without saying so.
func TestPlacePoisonedModule(t *testing.T) {
	root := writeTree(t, with(fixtureModule, map[string]string{"c/c.go": "package c\n\nimport \"example.com/fx/missing\"\n"}))
	pl, err := New(spellDirectives).Place(context.Background(), root, []string{"a/a.go", "README.md"})
	require.NoError(t, err)
	assert.Empty(t, pl.Packages)
	assert.Contains(t, pl.Unplaced["a/a.go"], "example.com/fx/missing")
	assert.Contains(t, pl.Unplaced["README.md"], "go list reported a load error in module .")
}

// TestPlaceOutsideAnyModule: a path no go.mod encloses is not the Go prover's to place.
func TestPlaceOutsideAnyModule(t *testing.T) {
	root := writeTree(t, map[string]string{"docs/x.md": "# x\n", "tool/t.go": "package t\n"})
	pl, err := New(spellDirectives).Place(context.Background(), root, []string{"docs/x.md", "tool/t.go"})
	require.NoError(t, err)
	assert.Empty(t, pl.Packages)
	assert.Empty(t, pl.Unplaced)
}

// TestPlaceListFailure: a module go list cannot load leaves its paths unplaced with the
// failure, rather than failing the other modules' placement.
func TestPlaceListFailure(t *testing.T) {
	root := writeTree(t, fixtureModule)
	p := Prover{Directives: spellDirectives, List: func(context.Context, string) ([]byte, error) {
		return nil, os.ErrPermission
	}}
	pl, err := p.Place(context.Background(), root, []string{"a/a.go"})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"a/a.go": "go list failed in module ., so what compiles or embeds it is unknown: permission denied"}, pl.Unplaced)
}

func TestEquivalent(t *testing.T) {
	const base = "//go:build linux\n\npackage p\n\nimport \"fmt\"\n\n// F prints.\nfunc F(a, b int) {\n\tfmt.Println(a + b)\n}\n"
	tests := []struct {
		name string
		path string
		cur  string
		want bool
	}{
		{"identical", "p.go", base, true},
		{"comment only", "p.go", strings.Replace(base, "// F prints.", "// F prints the sum,\n// then returns.", 1), true},
		{"gofmt only", "p.go", "//go:build linux\n\npackage p\nimport \"fmt\"\n// F prints.\nfunc F(a,b int){fmt.Println(a+b)}\n", true},
		{"code change", "p.go", strings.Replace(base, "a + b", "a - b", 1), false},
		{"renamed parameter", "p.go", strings.Replace(base, "(a, b int)", "(x, b int)", 1), false},
		{"build constraint", "p.go", strings.Replace(base, "linux", "darwin", 1), false},
		{"directive added", "p.go", strings.Replace(base, "// F prints.", "//go:noinline", 1), false},
		{"does not parse", "p.go", base + "func {", false},
		{"not Go", "p.txt", base, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, why := New(spellDirectives).Equivalent(tt.path, base, tt.cur)
			assert.Equal(t, tt.want, got)
			if got {
				assert.Equal(t, "comment or format only: its comment-free syntax tree and directives equal the base's", why)
			}
		})
	}
}

func TestEquivalentCgoPreamble(t *testing.T) {
	const base = "package p\n\n// #include <stdio.h>\nimport \"C\"\n"
	got, _ := New(spellDirectives).Equivalent("p.go", base, "package p\n\n// #include <stdlib.h>\nimport \"C\"\n")
	assert.False(t, got, "cgo reads the preamble as C source")
}
