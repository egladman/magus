// Package testlayout defines two analyzers over the layout of Go test files.
//
// [Pairing] reports every X_test.go with no X.go beside it. It has no options and
// no exemptions: a test file pairs with a source file of the same stem, with that
// stem less its build suffixes, or with a platform family, and with nothing else.
// It reports under its own name, testpair, so a //nolint:testlayout written for
// one of the rules below cannot silence it.
//
// [Analyzer] and [New] carry the rest: a test file in an external test package,
// and, as options, a file named with a _unix segment and a test in package main.
//
// Neither depends on a linter runner. The golangci-lint plugin lives in the plugin
// subpackage.
//
// Both read beyond the pass. A test file a build constraint excludes is still
// checked, from the pass's ignored files, and source names come off disk, so a
// driver caching results against declared package inputs (go vet's unitchecker)
// can replay a stale verdict after you add or remove a sibling file.
package testlayout

import (
	"go/ast"
	"go/parser"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// externalPackageMessage names the fix for a test that sits outside the package it
// tests. It says what to do rather than what is wrong, because the wrong thing here
// compiles and passes: only a reader ever objects.
func externalPackageMessage(pkg string) string {
	inner := strings.TrimSuffix(pkg, "_test")
	return "test file declares `package " + pkg + "`; put it in `package " + inner +
		"` with the code it tests. If that closes an import cycle - the test needs a package " +
		"that imports " + inner + " back - move the test to the package that already sits above " +
		"both, or keep the external package with a //nolint:testlayout naming the cycle."
}

// isMainTestPackage reports whether name is the package clause of a test file
// that can only run inside the binary it drives: package main itself, or its
// external main_test variant.
func isMainTestPackage(name string) bool {
	return name == "main" || name == "main_test"
}

// mainTestMessage names the fix for [Options.ReportMainTests]: the wrong thing here
// also compiles and passes, so the message says where the logic belongs rather
// than what is wrong with leaving it in main.
const mainTestMessage = "move the logic this test drives into the package that owns it, then test it there"

const doc = `check where a test file lives and how it is named

It reports a test file in an external test package (package foo_test). A test
belongs in the package it tests; the external package is reserved for the case where
an in-package test would close an import cycle, and that case is worth a //nolint
naming the cycle rather than a silent convention.

With report-unix-suffix it reports any Go file named with a _unix segment. With
report-main-tests it reports any _test.go declaring package main or main_test: that
test can only run inside the binary it drives, which usually means the code it
drives never left main either.

Pairing a test file with its source file is testpair's job, not this one's.`

// buildSuffixes are trailing segments the Go build system reads as a constraint
// rather than part of the name, so rawconn_linux_test.go still pairs with
// rawconn.go.
//
// Every entry is a GOOS or GOARCH from `go tool dist list` on Go 1.26, plus
// `unix`, which go/build honors in a //go:build line but never in a file name,
// so a _unix.go file carries its constraint as a tag. An earlier version
// also carried `generic`, `other`, `stub`, `posix`, and `asm` because they read
// like build tags. None is one, and each made resolver_generic_test.go beside
// resolver.go silently exempt. Nobody reports a linter for staying quiet, so a
// suffix earns its place only when the toolchain acts on it.
var buildSuffixes = []string{
	"aix", "android", "darwin", "dragonfly", "freebsd", "illumos", "ios", "js",
	"linux", "netbsd", "openbsd", "plan9", "solaris", "wasip1", "windows",
	"386", "amd64", "arm", "arm64", "loong64", "mips", "mips64", "mips64le",
	"mipsle", "ppc64", "ppc64le", "riscv64", "s390x", "wasm",
	"unix",
}

// Options configures the analyzer returned by [New]. The json tags are golangci-lint's
// settings block: the plugin decodes straight into this struct rather than keeping a
// parallel copy, so adding an option here cannot be silently dropped on the way in.
//
// Every option is off in the zero value, and setting a flag true turns on what its
// name says, so a settings block lists only what it enables. Nothing here touches
// pairing, which [Pairing] does with no options at all.
type Options struct {
	// ReportUnixSuffix reports every Go file, test or source, whose name ends in
	// a _unix segment. The toolchain reads no constraint from that segment, so
	// the file's //go:build line decides what it serves and the name only
	// suggests it; name the platforms instead (X_linux.go, X_darwin.go,
	// X_other.go).
	ReportUnixSuffix bool `json:"report-unix-suffix"`

	// ReportMainTests reports every _test.go file declaring package main or
	// main_test, so a test that only compiles inside the binary it drives gets
	// flagged for the logic to move to a package of its own. A tree moving that
	// logic out over time would see every test the move has not reached yet.
	ReportMainTests bool `json:"report-main-tests"`
}

// New returns the testlayout analyzer configured by opts.
func New(opts Options) *analysis.Analyzer {
	l := linter{unixSuffix: opts.ReportUnixSuffix, mainTests: opts.ReportMainTests}

	return &analysis.Analyzer{Name: "testlayout", Doc: doc, Run: l.run}
}

// Analyzer is the testlayout analyzer with every option off: it reports external
// test packages only. Use [New] to turn the options on.
var Analyzer = New(Options{})

type linter struct {
	unixSuffix bool
	mainTests  bool
}

func (l linter) run(pass *analysis.Pass) (any, error) {
	for path, f := range goFiles(pass) {
		name := filepath.Base(path)
		if l.unixSuffix && unixSuffixed(name) {
			pass.Report(analysis.Diagnostic{Pos: f.Package, Message: unixSuffixMessage(name)})
		}

		if !strings.HasSuffix(name, "_test.go") {
			continue
		}

		if strings.HasSuffix(f.Name.Name, "_test") {
			pass.Report(analysis.Diagnostic{Pos: f.Package, Message: externalPackageMessage(f.Name.Name)})
		}

		if l.mainTests && isMainTestPackage(f.Name.Name) {
			pass.Report(analysis.Diagnostic{Pos: f.Package, Message: mainTestMessage})
		}
	}

	return nil, nil
}

// goFiles yields every Go file of the pass by path: the files it loaded, then the
// ones a build constraint kept out, parsed through the package clause so a
// diagnostic still lands on a real position. Without the second half a test file
// tagged for another platform, or for an opt-in tag, is never seen at all.
//
// A file with no position (a synthesized or overlay-sourced AST), or an ignored
// file that cannot be read or parsed, has nothing to reason about and is skipped
// rather than failing the run.
func goFiles(pass *analysis.Pass) func(yield func(string, *ast.File) bool) {
	return func(yield func(string, *ast.File) bool) {
		for _, f := range pass.Files {
			tf := pass.Fset.File(f.Pos())
			if tf == nil {
				continue
			}

			if !yield(tf.Name(), f) {
				return
			}
		}

		read := pass.ReadFile
		if read == nil {
			read = os.ReadFile
		}

		for _, path := range pass.IgnoredFiles {
			if !strings.HasSuffix(path, ".go") {
				continue
			}

			content, err := read(path)
			if err != nil {
				continue
			}

			f, err := parser.ParseFile(pass.Fset, path, content, parser.PackageClauseOnly)
			if err != nil {
				continue
			}

			if !yield(path, f) {
				return
			}
		}
	}
}

// unixSuffixed reports whether name, less .go and _test, ends in build suffixes of
// which one is unix: relay_unix.go and relay_unix_amd64_test.go, but not unix.go.
func unixSuffixed(name string) bool {
	base := strings.TrimSuffix(strings.TrimSuffix(name, ".go"), "_test")
	for {
		i := strings.LastIndex(base, "_")
		if i < 0 {
			return false
		}

		switch segment := base[i+1:]; {
		case segment == "unix":
			return true
		case !slices.Contains(buildSuffixes, segment):
			return false
		}

		base = base[:i]
	}
}

func unixSuffixMessage(name string) string {
	stem := strings.TrimSuffix(strings.TrimSuffix(name, ".go"), "_test")
	stem = stem[:strings.LastIndex(stem, "_unix")]

	return name + " is named for unix, which a file name does not constrain; name the platforms " +
		"it serves, " + stem + "_linux.go and " + stem + "_darwin.go, with " + stem +
		"_other.go for the rest and " + stem + ".go for what they share"
}

// trimBuildSuffixes removes every trailing segment the Go build system reads as a
// constraint, so rawconn_unix and bytes_js_wasm both come back as the name the
// source file carries.
func trimBuildSuffixes(base string) string {
	for {
		i := strings.LastIndex(base, "_")
		if i < 0 || !slices.Contains(buildSuffixes, base[i+1:]) {
			return base
		}

		base = base[:i]
	}
}
