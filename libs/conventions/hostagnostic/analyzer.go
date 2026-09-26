// Package hostagnostic reports a line of non-test Go source that names an
// agent host anywhere but inside a filesystem path.
//
// Naming the directory a host discovers skills in is the one host-specific
// step magus owns. Setup instructions, help text and a per-host branch belong
// in documentation the reader owns, or the next change to that host becomes a
// magus release.
//
// The scan is by line, comments included, over every file of the package
// whatever its build constraints.
package hostagnostic

import (
	"regexp"
	"slices"
	"strings"

	"github.com/egladman/magus/libs/conventions/internal/source"
	"golang.org/x/tools/go/analysis"
)

// hostNames are the hosts matched by bare word. Cursor is absent: the word is a
// terminal position and a pagination token far more often than the host, so
// cursorHostUse carries it instead.
var hostNames = regexp.MustCompile(`(?i)\b(claude|opencode|codex|aider|windsurf)\b`) //nolint:hostagnostic // the rule's own host list

// cursorHostUse matches the shapes "cursor" takes when it means the host: the
// proper noun in parentheses, a phrase naming the host's machinery, and a
// comparison against the host label. A bare `case "cursor":` is not matched,
// since no line-level pattern separates it from a diff-session or memory op.
var cursorHostUse = regexp.MustCompile(`\(Cursor\)|(?i:\bcursor (hooks?|ide|editor|rules)\b|[!=]=\s*"cursor")`)

// hostPathUse allows a host name that names something on disk: a path, or a
// bare quoted filename stem such as the "claude" classifying a host's
// instruction file.
var hostPathUse = regexp.MustCompile(`(?i)([./~][a-z0-9_.-]*\b(claude|opencode|codex|aider|windsurf)\b[a-z0-9_.-]*)|("(claude|opencode|codex|aider|windsurf)")`) //nolint:hostagnostic // the rule's own host list

const message = "names an agent host outside a filesystem path: magus may name a host only in a path " +
	"such as .claude/skills; move setup instructions, help text or a per-host branch into docs the reader owns"

// Options configures the analyzer returned by [New].
type Options struct {
	// Module is the import path [SkipDirs] is relative to. A package outside it
	// is matched on its own import path.
	Module string `json:"module"`

	// SkipDirs names directories, by any segment of the module-relative path,
	// whose files the rule does not govern: generated output, fixtures, and
	// embedded documentation.
	SkipDirs []string `json:"skip-dirs"`
}

// New returns the analyzer configured by opts.
func New(opts Options) (*analysis.Analyzer, error) {
	return &analysis.Analyzer{
		Name: "hostagnostic",
		Doc:  "report Go source that names an agent host outside a filesystem path",
		Run:  func(pass *analysis.Pass) (any, error) { return nil, run(pass, opts) },
	}, nil
}

// Line reports whether one line of Go source names an agent host outside a
// filesystem path.
func Line(text string) bool {
	if cursorHostUse.MatchString(text) {
		return true
	}
	// A line may carry both a path and prose; strip the paths, then re-test.
	return hostNames.MatchString(text) && hostNames.MatchString(hostPathUse.ReplaceAllString(text, ""))
}

func run(pass *analysis.Pass, opts Options) error {
	files, err := source.Files(pass)
	if err != nil {
		return err
	}
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
		for i, line := range strings.Split(string(src), "\n") {
			if Line(strings.TrimSuffix(line, "\r")) {
				pass.Reportf(tf.LineStart(i+1), "%s", message)
			}
		}
	}
	return nil
}

func skipped(rel string, dirs []string) bool {
	segs := strings.Split(rel, "/")
	return slices.ContainsFunc(segs[:len(segs)-1], func(s string) bool { return slices.Contains(dirs, s) })
}
