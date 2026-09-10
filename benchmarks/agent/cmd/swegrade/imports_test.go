package main

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// TestSwegradeImportsOnlyTheStandardLibrary pins what grader.Dockerfile relies
// on: it copies this directory's sources into a throwaway module and builds
// them with no dependencies. An import from any module, this repository's
// included, would compile under the root module and fail only when the grader
// image is built, several minutes into a run. A standard library path has no
// dot in its first element, which is how the toolchain tells the two apart.
func TestSwegradeImportsOnlyTheStandardLibrary(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			first, _, _ := strings.Cut(path, "/")
			if strings.Contains(first, ".") {
				t.Errorf("%s imports %s, which the grader image cannot build", name, path)
			}
		}
	}
}
