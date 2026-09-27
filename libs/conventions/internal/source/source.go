// Package source gives the conventions analyzers the files a tree walk would
// see: a package's compiled files plus the ones its build constraints exclude
// on this platform, each addressed by its path inside the module.
package source

import (
	"fmt"
	"go/ast"
	"go/parser"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/mod/modfile"
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

// RequireMatches errors, naming linter and setting, on a pattern that matches
// no file under root. A scope that matches nothing reports nothing, so a moved
// file would otherwise turn the rule off without a word.
func (g Globs) RequireMatches(linter, setting, root string) error {
	for _, p := range g {
		matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil {
			return fmt.Errorf("%s: %s pattern %q: %w", linter, setting, p, err)
		}
		if len(matches) == 0 {
			return fmt.Errorf("%s: %s pattern %q matches no file under %s; the code it scoped moved, fix the setting",
				linter, setting, p, root)
		}
	}
	return nil
}

// RequirePackage errors, naming linter and setting, when the import path pkg
// inside module has no Go files under root.
func RequirePackage(linter, setting, root, module, pkg string) error {
	dir := root
	if pkg != module {
		rel, ok := strings.CutPrefix(pkg, module+"/")
		if !ok {
			return fmt.Errorf("%s: %s %q is outside module %s", linter, setting, pkg, module)
		}
		dir = filepath.Join(root, filepath.FromSlash(rel))
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, "*.go")); len(matches) == 0 {
		return fmt.Errorf("%s: %s %q has no Go files under %s; the package moved, fix the setting",
			linter, setting, pkg, root)
	}
	return nil
}

// GoFiles returns the slash-separated path, relative to root, of every .go file
// the go tool would read under root, nested modules included. Like the go tool,
// it skips a file or directory whose name begins with "." or "_", and any
// directory named testdata.
func GoFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		name := d.Name()
		ignored := strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
		switch {
		case d.IsDir() && (ignored || name == "testdata"):
			return filepath.SkipDir
		case d.IsDir(), ignored, !strings.HasSuffix(name, ".go"):
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

// RequireDirNames errors, naming linter and setting, on a name that is no
// directory on the path of any of files, as [GoFiles] returns them for root.
// A skip entry that skips nothing is one the tree moved out from under.
func RequireDirNames(linter, setting, root string, names, files []string) error {
	dirs := map[string]bool{}
	for _, f := range files {
		segs := strings.Split(f, "/")
		for _, s := range segs[:len(segs)-1] {
			dirs[s] = true
		}
	}
	for _, n := range names {
		if !dirs[n] {
			return fmt.Errorf("%s: %s entry %q names no directory holding Go files under %s; it skips nothing, fix the setting",
				linter, setting, n, root)
		}
	}
	return nil
}

// RequireDirs errors, naming linter and setting, on a slash-separated directory
// relative to root that holds none of files at or below it, as [GoFiles]
// returns them for root.
func RequireDirs(linter, setting, root string, dirs, files []string) error {
	for _, d := range dirs {
		if !slices.ContainsFunc(files, func(f string) bool { return strings.HasPrefix(f, d+"/") }) {
			return fmt.Errorf("%s: %s entry %q holds no Go files under %s; the code it named moved, fix the setting",
				linter, setting, d, root)
		}
	}
	return nil
}

// Hint appends a repository's own remedy to a diagnostic. The analyzers word
// their messages without naming any one repository's identifiers or paths,
// and the configuration supplies those.
func Hint(message, hint string) string {
	if hint == "" {
		return message
	}
	return message + "; " + hint
}

// Root returns the directory whose go.mod declares module, searching up from
// the working directory. golangci-lint runs a nested module from its own
// directory, and this still resolves the enclosing module's paths from there.
func Root(linter, module string) (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("%s: %w", linter, err)
	}
	for dir := wd; ; dir = filepath.Dir(dir) {
		if data, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil && modfile.ModulePath(data) == module {
			return dir, nil
		}
		if filepath.Dir(dir) == dir {
			return "", fmt.Errorf("%s: module %q: no go.mod at or above %s declares it", linter, module, wd)
		}
	}
}

// InModule runs check against module's [Root]. An empty module skips it: an
// analysistest package has no module to resolve.
func InModule(linter, module string, check func(root string) error) error {
	if module == "" {
		return nil
	}
	root, err := Root(linter, module)
	if err != nil {
		return err
	}
	return check(root)
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
