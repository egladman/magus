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
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/egladman/magus/libs/conventions/internal/source"
	"golang.org/x/tools/go/analysis"
)

const message = "%s mashes %q and %q together, both already file names in this tree: name it %s.go, or %s_%s.go " +
	"if it covers both; if %s is one established word, add it to filenames' allow setting and say why"

// Options configures the analyzer returned by [New].
type Options struct {
	// Module names the tree: the directory whose go.mod declares it is walked,
	// nested modules included, and every Go file name under it is vocabulary.
	Module string `json:"module"`

	// SkipDirs names directories, by any segment of the root-relative path,
	// that neither supply vocabulary nor are checked.
	SkipDirs []string `json:"skip-dirs"`

	// Suffixes are stripped from a file name's end, repeatedly, before it is
	// split on underscores: "linux" makes jobserver_unix_test.go "jobserver".
	Suffixes []string `json:"suffixes"`

	// Allow names segments that are one established word.
	Allow []string `json:"allow"`
}

// New returns the analyzer configured by opts, erroring on an empty Module.
// The tree is walked once, on the first package analyzed.
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
	c := &checker{opts: opts, suffixes: suffixes}
	c.load = sync.OnceValues(c.walk)
	return &analysis.Analyzer{
		Name: "filenames",
		Doc:  "report Go file names that mash two words together",
		Run:  func(pass *analysis.Pass) (any, error) { return nil, c.run(pass) },
	}, nil
}

type tree struct {
	root     string
	segments map[string]bool
}

type checker struct {
	opts     Options
	suffixes *regexp.Regexp
	load     func() (tree, error)
}

func (c *checker) walk() (tree, error) {
	root, err := source.Root("filenames", c.opts.Module)
	if err != nil {
		return tree{}, err
	}
	t := tree{root: root, segments: map[string]bool{}}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && slices.Contains(c.opts.SkipDirs, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		for _, seg := range c.segments(d.Name()) {
			t.segments[seg] = true
		}
		return nil
	})
	if err != nil {
		return tree{}, fmt.Errorf("filenames: %w", err)
	}
	if len(t.segments) == 0 {
		return tree{}, fmt.Errorf("filenames: no Go files under %s; the rule would report nothing", root)
	}
	return t, nil
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
	t, err := c.load()
	if err != nil {
		return err
	}
	files, err := source.Files(pass)
	if err != nil {
		return err
	}
	for _, f := range files {
		name := source.Name(pass, f)
		if rel, err := filepath.Rel(t.root, filepath.Dir(name)); err == nil &&
			slices.ContainsFunc(strings.Split(filepath.ToSlash(rel), "/"), func(s string) bool { return slices.Contains(c.opts.SkipDirs, s) }) {
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
				if t.segments[head] && t.segments[tail] {
					pass.Reportf(f.Package, message, seg, head, tail, tail, head, tail, seg)
					break
				}
			}
		}
	}
	return nil
}
