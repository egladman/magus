// Package filenames reports a Go file name segment that mashes two words
// together: pushgate.go rather than push_gate.go or gate.go.
//
// The vocabulary is built from the tree rather than a dictionary: a segment is
// suspect when it splits into two segments the tree already uses as file names.
// skillgate, pushgate and hostschemas each shipped after the last had been
// corrected by hand. The rule misses a compound of words that appear nowhere
// else, and never reports a segment it cannot split into two known ones.
//
// No rule separates "runtime" from "pushgate": both are two known words with
// the separator dropped, and only a person knows the first is one word. The
// allow setting is where that is said.
package filenames

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/egladman/magus/libs/conventions/internal/source"
	"golang.org/x/tools/go/analysis"
)

const message = "%s mashes %q and %q together, both already file names in this tree: name it %s.go, or %s_%s.go " +
	"if it covers both; if %s is one established word, add it to filenames' allow setting and say why"

// Options configures the analyzer returned by [New].
type Options struct {
	// Module names the tree: every Go file name the go tool would read under
	// the directory whose go.mod declares it, nested modules included, is
	// vocabulary.
	Module string `json:"module"`

	// SkipDirs names directories, by any segment of the root-relative path,
	// that neither supply vocabulary nor are checked. Each must name a
	// directory holding Go files.
	SkipDirs []string `json:"skip-dirs"`

	// Suffixes are stripped from a file name's end, repeatedly, before it is
	// split on underscores: "linux" makes jobserver_unix_test.go "jobserver".
	Suffixes []string `json:"suffixes"`

	// Allow names segments that are one established word.
	Allow []string `json:"allow"`
}

// New returns the analyzer configured by opts, reading the tree's file names
// once. It errors on an empty Module, a tree with no Go files, and a SkipDirs
// entry that skips nothing.
func New(opts Options) (*analysis.Analyzer, error) {
	if opts.Module == "" {
		return nil, errors.New("filenames: module is required: its tree's file names are the vocabulary")
	}
	var suffixes *regexp.Regexp
	if len(opts.Suffixes) > 0 {
		quoted := make([]string, len(opts.Suffixes))
		for i, s := range opts.Suffixes {
			quoted[i] = regexp.QuoteMeta(s)
		}
		suffixes = regexp.MustCompile(`_(` + strings.Join(quoted, "|") + `)$`)
	}
	c := &checker{opts: opts, suffixes: suffixes, vocabulary: map[string]bool{}}
	root, err := source.Root("filenames", opts.Module)
	if err != nil {
		return nil, err
	}
	c.root = root
	files, err := source.GoFiles(root)
	if err != nil {
		return nil, fmt.Errorf("filenames: %w", err)
	}
	if err := source.RequireDirNames("filenames", "skip-dirs", root, opts.SkipDirs, files); err != nil {
		return nil, err
	}
	for _, rel := range files {
		if c.skipped(rel) {
			continue
		}
		for _, seg := range c.segments(filepath.Base(rel)) {
			c.vocabulary[seg] = true
		}
	}
	if len(c.vocabulary) == 0 {
		return nil, fmt.Errorf("filenames: no Go files under %s; the rule would report nothing", root)
	}
	return &analysis.Analyzer{
		Name: "filenames",
		Doc:  "report Go file names that mash two words together",
		Run:  func(pass *analysis.Pass) (any, error) { return nil, c.run(pass) },
	}, nil
}

type checker struct {
	opts       Options
	suffixes   *regexp.Regexp
	root       string
	vocabulary map[string]bool
}

// skipped reports whether a slash-separated root-relative file path sits in a
// SkipDirs directory.
func (c *checker) skipped(rel string) bool {
	segs := strings.Split(rel, "/")
	return slices.ContainsFunc(segs[:len(segs)-1], func(s string) bool { return slices.Contains(c.opts.SkipDirs, s) })
}

// segments returns the underscore-separated words of a Go file name, or nil
// for any other file.
func (c *checker) segments(name string) []string {
	base, ok := strings.CutSuffix(name, ".go")
	if !ok {
		return nil
	}
	if c.suffixes != nil {
		for prev := ""; prev != base; {
			prev, base = base, c.suffixes.ReplaceAllString(base, "")
		}
	}
	return slices.DeleteFunc(strings.Split(base, "_"), func(s string) bool { return s == "" })
}

func (c *checker) run(pass *analysis.Pass) error {
	files, err := source.Files(pass)
	if err != nil {
		return err
	}
	for _, f := range files {
		name := source.Name(pass, f)
		if rel, err := filepath.Rel(c.root, name); err == nil && c.skipped(filepath.ToSlash(rel)) {
			continue
		}
		for _, seg := range c.segments(filepath.Base(name)) {
			if slices.Contains(c.opts.Allow, seg) {
				continue
			}
			// Both halves at least two letters, so a stray prefix such as the
			// "a" of "async" is never a word.
			for i := 2; i < len(seg)-1; i++ {
				head, tail := seg[:i], seg[i:]
				if c.vocabulary[head] && c.vocabulary[tail] {
					pass.Reportf(f.Package, message, seg, head, tail, tail, head, tail, seg)
					break
				}
			}
		}
	}
	return nil
}
