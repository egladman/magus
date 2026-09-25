package risk

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixtureModule is a module whose import graph every test below reasons about:
//
//	a <- b <- c <- app (main)     e's TEST imports a
//	d runs `go run ../gen`        gen (main) <- genlib
//	skills embeds doc.md          w builds only on windows
var fixtureModule = map[string]string{
	"go.mod":                     "module example.com/fx\n\ngo 1.25\n",
	"a/a.go":                     "package a\n\n// A is the leaf.\nfunc A() int { return 1 }\n",
	"b/b.go":                     "package b\n\nimport \"example.com/fx/a\"\n\nfunc B() int { return a.A() }\n",
	"c/c.go":                     "package c\n\nimport \"example.com/fx/b\"\n\nfunc C() int { return b.B() }\n",
	"app/main.go":                "package main\n\nimport \"example.com/fx/c\"\n\nfunc main() { _ = c.C() }\n",
	"d/d.go":                     "package d\n\n//go:generate go run ../gen\n\nfunc D() {}\n",
	"e/e.go":                     "package e\n\nfunc E() {}\n",
	"e/e_test.go":                "package e\n\nimport (\n\t\"testing\"\n\n\t\"example.com/fx/a\"\n)\n\nfunc TestE(t *testing.T) { _ = a.A() }\n",
	"e/zz_generated.go":          "package e\n\nfunc Generated() {}\n",
	"gen/main.go":                "package main\n\nimport \"example.com/fx/genlib\"\n\nfunc main() { genlib.Run() }\n",
	"genlib/genlib.go":           "package genlib\n\nfunc Run() {}\n",
	"skills/skills.go":           "package skills\n\nimport _ \"embed\"\n\n//go:embed doc.md\nvar Doc string\n",
	"skills/doc.md":              "# Skill\n",
	"w/w.go":                     "package w\n",
	"w/w_windows.go":             "package w\n",
	"a/testdata/x.go":            "package fixture\n",
	"changes/unreleased/risk.md": "Added risk.\n",
	"README.md":                  "# fx\n",
	"magusfile.buzz":             "export fun ci(ctx: magus\\Context, args: [str]) > void {}\n",
	"tools/gen.yaml":             "a: 1\n",
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

func loadFixture(t *testing.T, root string) *goModule {
	t.Helper()
	out, err := GoList(context.Background(), root)
	require.NoError(t, err)
	pkgs, err := decodeGoList(out)
	require.NoError(t, err)
	return newGoModule(root, pkgs)
}

func TestClosureMatchesGoListReverseDeps(t *testing.T) {
	root := writeTree(t, fixtureModule)
	m := loadFixture(t, root)
	require.Empty(t, m.poison)

	// The oracle is go's own answer, package by package: P is in the closure of a
	// exactly when `go list -deps -test P` names a.
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

	assert.Equal(t, want, m.closure(map[string]bool{"example.com/fx/a": true}, nil))
	assert.Equal(t, []string{"example.com/fx/a", "example.com/fx/app", "example.com/fx/b", "example.com/fx/c", "example.com/fx/e"}, want)
}

func TestGoModuleIndex(t *testing.T) {
	root := writeTree(t, fixtureModule)
	m := loadFixture(t, root)

	hit := func(p string) goFileHit { return m.files[filepath.Join(root, filepath.FromSlash(p))] }
	assert.Equal(t, goFileHit{pkg: "example.com/fx/b", role: goCompiled}, hit("b/b.go"))
	assert.Equal(t, goFileHit{pkg: "example.com/fx/e", role: goTestOnly}, hit("e/e_test.go"))
	assert.Equal(t, goFileHit{pkg: "example.com/fx/skills", role: goCompiled}, hit("skills/doc.md"))
	assert.Equal(t, goFileHit{pkg: "example.com/fx/w", role: goIgnored}, hit("w/w_windows.go"))
	assert.Equal(t, goFileHit{}, hit("a/testdata/x.go"))
	assert.Equal(t, map[string]string{"example.com/fx/gen": "example.com/fx/gen", "example.com/fx/genlib": "example.com/fx/gen"}, m.generators)
	assert.Equal(t, []string{"example.com/fx/e"}, m.closure(nil, map[string]bool{"example.com/fx/e": true}))
}

func TestGoModulePoisonedByLoadError(t *testing.T) {
	files := with(fixtureModule, map[string]string{"c/c.go": "package c\n\nimport \"example.com/fx/missing\"\n"})
	m := loadFixture(t, writeTree(t, files))
	assert.Contains(t, m.poison, "example.com/fx/missing")
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

func TestGoEquivalent(t *testing.T) {
	const base = "//go:build linux\n\npackage p\n\nimport \"fmt\"\n\n// F prints.\nfunc F(a, b int) {\n\tfmt.Println(a + b)\n}\n"
	tests := []struct {
		name string
		cur  string
		want bool
	}{
		{"identical", base, true},
		{"comment only", strings.Replace(base, "// F prints.", "// F prints the sum,\n// then returns.", 1), true},
		{"gofmt only", "//go:build linux\n\npackage p\nimport \"fmt\"\n// F prints.\nfunc F(a,b int){fmt.Println(a+b)}\n", true},
		{"code change", strings.Replace(base, "a + b", "a - b", 1), false},
		{"renamed parameter", strings.Replace(base, "(a, b int)", "(x, b int)", 1), false},
		{"build constraint", strings.Replace(base, "linux", "darwin", 1), false},
		{"directive added", strings.Replace(base, "// F prints.", "//go:noinline", 1), false},
		{"does not parse", base + "func {", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, goEquivalent(base, tt.cur, spellDirectives))
		})
	}
}

func TestGoEquivalentCgoPreamble(t *testing.T) {
	const base = "package p\n\n// #include <stdio.h>\nimport \"C\"\n"
	assert.False(t, goEquivalent(base, "package p\n\n// #include <stdlib.h>\nimport \"C\"\n", spellDirectives))
}
