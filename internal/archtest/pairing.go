// Package archtest holds repo-wide structural checks: conventions the
// compiler cannot enforce but that the codebase relies on staying true.
// Each check is a plain function returning its violations, so a test can
// assert on them directly.
package archtest

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// skipDirs are directory names whose contents are not hand-authored Go
// source subject to repo conventions: dependency trees, generated
// output, test fixtures, and detached worktrees that duplicate the tree.
var skipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	"testdata":     true,
	"gen":          true,
	".claude":      true,
	".testcache":   true,
}

// goosSuffixes are the build-constraint suffixes Go itself gives meaning
// to in a file name. A _test.go carrying one targets a platform whose
// source file may legitimately be named without the suffix (or not exist
// at all, when the test covers cross-platform code from one platform).
var goosSuffixes = []string{
	"_aix", "_android", "_darwin", "_dragonfly", "_freebsd", "_hurd",
	"_illumos", "_ios", "_js", "_linux", "_netbsd", "_openbsd", "_plan9",
	"_solaris", "_wasip1", "_windows", "_unix",
}

// exemptKinds name a category of test rather than a source file, so no
// counterpart is implied. A stem matches either exactly ("bench_test.go")
// or as a trailing qualifier ("hash_bench_test.go"), which is why one
// list covers both shapes: keeping two would let them drift apart.
//
// Keep this short. Every entry is a hole in the pairing rule.
//
//   - example: `go test` treats Example functions as documentation, and
//     gathering them in one file is the established layout.
//   - bench / fuzz: benchmark and fuzz corpora routinely span a whole
//     package rather than shadowing a single file.
//   - internal: the conventional name for a white-box test of a package
//     whose other tests live in the _test package.
//   - integration / conformance / drift / parity: suites that exercise a
//     whole subsystem or compare it against an external source of truth.
var exemptKinds = []string{
	"example", "examples", "bench", "fuzz", "internal",
	"integration", "conformance", "drift", "parity",
}

// FindUnpairedTests returns every _test.go file under root whose source
// counterpart is missing, as slash-separated paths relative to root.
//
// The convention: foo_test.go tests foo.go and sits beside it, so a
// reader opening either finds the other, and a renamed source file drags
// its tests along instead of stranding them under a name that no longer
// matches anything. Files exempted by [exemptKinds] or a GOOS
// build-constraint suffix are not reported.
//
// Results come back in walk order (lexical within each directory), which
// keeps failures stable and diffable across runs.
//
// The walk covers a whole repository, so ctx lets a caller abandon it;
// cancellation is reported as ctx.Err().
func FindUnpairedTests(ctx context.Context, root string) ([]string, error) {
	var unpaired []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, "_test.go") {
			return nil
		}
		stem := strings.TrimSuffix(name, "_test.go")
		if isExempt(stem) {
			return nil
		}
		// The counterpart must sit in the same directory: a test beside
		// its source is the whole point of the convention.
		switch _, statErr := os.Stat(filepath.Join(filepath.Dir(path), stem+".go")); {
		case statErr == nil:
			return nil
		case !errors.Is(statErr, fs.ErrNotExist):
			// A permission error or symlink loop is not evidence that the
			// counterpart is missing; surface it rather than reporting a
			// false violation.
			return statErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		unpaired = append(unpaired, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return unpaired, nil
}

// isExempt reports whether a _test.go stem is excused from needing a
// source counterpart.
func isExempt(stem string) bool {
	if slices.ContainsFunc(exemptKinds, func(k string) bool {
		return stem == k || strings.HasSuffix(stem, "_"+k)
	}) {
		return true
	}
	return slices.ContainsFunc(goosSuffixes, func(s string) bool {
		return strings.HasSuffix(stem, s)
	})
}
