// Package source gives the conventions analyzers the files a tree walk would
// see: a package's compiled files plus the ones its build constraints exclude
// on this platform, each addressed by its path inside the module.
package source

import (
	"fmt"
	"go/ast"
	"go/parser"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// Files returns pass.Files followed by every ignored .go file, parsed with
// comments into pass.Fset. A rule over source text holds on every platform, so
// a darwin run still reads the _linux.go files.
func Files(pass *analysis.Pass) ([]*ast.File, error) {
	files := append([]*ast.File(nil), pass.Files...)
	for _, name := range pass.IgnoredFiles {
		if !strings.HasSuffix(name, ".go") {
			continue
		}
		src, err := pass.ReadFile(name)
		if err != nil {
			return nil, err
		}
		f, err := parser.ParseFile(pass.Fset, name, src, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			// A file excluded here may not even parse under this toolchain; the
			// platform that compiles it reports that.
			continue
		}
		files = append(files, f)
	}
	return files, nil
}

// Name is the filename f was parsed from.
func Name(pass *analysis.Pass, f *ast.File) string {
	return pass.Fset.File(f.FileStart).Name()
}

// IsTest reports whether f is a _test.go file.
func IsTest(pass *analysis.Pass, f *ast.File) bool {
	return strings.HasSuffix(Name(pass, f), "_test.go")
}

// Rel returns f's slash-separated path inside module, derived from the package
// import path rather than the filesystem so a checkout's own location never
// matters. It reports false for a package outside module. An empty module
// treats the import path itself as the path.
func Rel(pass *analysis.Pass, module string, f *ast.File) (string, bool) {
	pkg := strings.TrimSuffix(pass.Pkg.Path(), "_test")
	dir := pkg
	if module != "" {
		switch {
		case pkg == module:
			dir = ""
		case strings.HasPrefix(pkg, module+"/"):
			dir = pkg[len(module)+1:]
		default:
			return "", false
		}
	}
	return path.Join(dir, filepath.Base(Name(pass, f))), true
}

// Globs is a list of [path.Match] patterns over [Rel] paths.
type Globs []string

// Validate rejects a malformed pattern at construction, where a config typo
// names itself, rather than on the first file it meets.
func (g Globs) Validate(linter string) error {
	for _, p := range g {
		if _, err := path.Match(p, "probe"); err != nil {
			return fmt.Errorf("%s: pattern %q: %w", linter, p, err)
		}
	}
	return nil
}

// Match reports whether rel matches any pattern.
func (g Globs) Match(rel string) bool {
	for _, p := range g {
		if ok, _ := path.Match(p, rel); ok {
			return true
		}
	}
	return false
}
