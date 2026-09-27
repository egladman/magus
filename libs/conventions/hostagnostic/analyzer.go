// Package hostagnostic reports a line of non-test Go source that names an
// agent host anywhere but inside a filesystem path.
//
// Naming the directory a host discovers files in is the one host-specific step
// a host-agnostic tool owns. Setup instructions, help text and a per-host branch
// belong in documentation the reader owns, or the next change to that host
// becomes a release of the tool.
//
// The scan is by line, comments included, over every file of the package
// whatever its build constraints. Which hosts count is configuration.
package hostagnostic

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/egladman/magus/libs/conventions/internal/source"
	"golang.org/x/tools/go/analysis"
)

const message = "names an agent host outside a filesystem path: name a host only in a path, " +
	"and move setup instructions, help text or a per-host branch into docs the reader owns"

// Options configures the analyzer returned by [New].
type Options struct {
	// Module is the import path [Options.SkipDirs] is relative to. A package
	// outside it is matched on its own import path. When set, every SkipDirs
	// entry must name a directory holding Go files under the module's root.
	Module string `json:"module"`

	// SkipDirs names directories, by any segment of the module-relative path,
	// whose files the rule does not govern: generated output, fixtures, and
	// embedded documentation.
	SkipDirs []string `json:"skip-dirs"`

	// Hosts are agent host names matched as whole words, case folded. A name
	// inside a filesystem path, or quoted alone as a filename stem, is allowed.
	Hosts []string `json:"hosts"`

	// HostPatterns are regular expressions for a host whose name is also an
	// ordinary word, each matching only a shape in which the word means the
	// host. A match is reported even beside a path.
	HostPatterns []string `json:"host-patterns"`

	// Hint is appended to every diagnostic: the repository's own remedy, which
	// may name its paths.
	Hint string `json:"hint"`
}

// New returns the analyzer configured by opts, erroring when no host is
// configured, on a pattern that does not compile, and on a SkipDirs entry
// that skips nothing.
func New(opts Options) (*analysis.Analyzer, error) {
	m, err := newMatcher(opts.Hosts, opts.HostPatterns)
	if err != nil {
		return nil, err
	}
	if err := source.InModule("hostagnostic", opts.Module, func(root string) error {
		files, err := source.GoFiles(root)
		if err != nil {
			return fmt.Errorf("hostagnostic: %w", err)
		}
		return source.RequireDirNames("hostagnostic", "skip-dirs", root, opts.SkipDirs, files)
	}); err != nil {
		return nil, err
	}
	return &analysis.Analyzer{
		Name: "hostagnostic",
		Doc:  "report Go source that names an agent host outside a filesystem path",
		Run:  func(pass *analysis.Pass) (any, error) { return nil, run(pass, opts, m) },
	}, nil
}

type matcher struct {
	names    *regexp.Regexp
	paths    *regexp.Regexp
	patterns []*regexp.Regexp
}

func newMatcher(hosts, patterns []string) (matcher, error) {
	if len(hosts) == 0 && len(patterns) == 0 {
		return matcher{}, errors.New("hostagnostic: hosts and host-patterns are both empty, so nothing would be reported")
	}
	var m matcher
	if len(hosts) > 0 {
		quoted := make([]string, len(hosts))
		for i, h := range hosts {
			quoted[i] = regexp.QuoteMeta(h)
		}
		alt := strings.Join(quoted, "|")
		m.names = regexp.MustCompile(`(?i)\b(` + alt + `)\b`)
		m.paths = regexp.MustCompile(`(?i)([./~][a-z0-9_.-]*\b(` + alt + `)\b[a-z0-9_.-]*)|("(` + alt + `)")`)
	}
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return matcher{}, fmt.Errorf("hostagnostic: host-patterns %q: %w", p, err)
		}
		m.patterns = append(m.patterns, re)
	}
	return m, nil
}

// line reports whether one line of Go source names an agent host outside a
// filesystem path.
func (m matcher) line(text string) bool {
	if slices.ContainsFunc(m.patterns, func(re *regexp.Regexp) bool { return re.MatchString(text) }) {
		return true
	}
	if m.names == nil {
		return false
	}
	// A line may carry both a path and prose; strip the paths, then re-test.
	return m.names.MatchString(text) && m.names.MatchString(m.paths.ReplaceAllString(text, ""))
}

func run(pass *analysis.Pass, opts Options, m matcher) error {
	files, err := source.Files(pass)
	if err != nil {
		return err
	}
	diagnostic := source.Hint(message, opts.Hint)
	for _, f := range files {
		// A test naming a host describes a real event rather than encoding a path.
		if source.IsTest(pass, f) {
			continue
		}
		rel, ok := source.Rel(pass, opts.Module, f)
		if !ok {
			// A module of its own name, such as a benchmark harness, is still in the
			// tree; its import path stands in for the relative path.
			rel, _ = source.Rel(pass, "", f)
		}
		if skipped(rel, opts.SkipDirs) {
			continue
		}
		name := source.Name(pass, f)
		src, err := pass.ReadFile(name)
		if err != nil {
			return err
		}
		tf := pass.Fset.File(f.FileStart)
		for i, text := range strings.Split(string(src), "\n") {
			if m.line(strings.TrimSuffix(text, "\r")) {
				pass.Reportf(tf.LineStart(i+1), "%s", diagnostic)
			}
		}
	}
	return nil
}

func skipped(rel string, dirs []string) bool {
	segs := strings.Split(rel, "/")
	return slices.ContainsFunc(segs[:len(segs)-1], func(s string) bool { return slices.Contains(dirs, s) })
}
