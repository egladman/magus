package std

import (
	"context"
	"fmt"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
)

//go:generate go run ../cmd/magus-utils bindings -module diff -lang buzz -out ../internal/interp/bindings/gen/diff.go

// diffDefaultContext is how many unchanged lines surround each hunk when the
// caller names none. Three is what `git diff` and `diff -u` both default to, and
// matching them means a magusfile's output is readable by every tool and person
// already used to reading a patch.
const diffDefaultContext = 3

func init() { Register(Diff) }

// Diff is the "diff" host module: compare two texts and say what changed.
//
// magus is in the drift business - a generate target is a drift gate, `magus
// affected ci` fails when committed output no longer matches regenerated output -
// and until now the language those gates are written in could not say WHAT
// differed. A magusfile could report "archive.md is out of date" and nothing more,
// which leaves the reader to regenerate locally and diff by hand to find out
// whether the change is theirs or a tool-version artifact.
//
// Unified format specifically, rather than a bespoke rendering: it is what git,
// `diff -u`, every code review tool and every developer already reads, and it
// pastes into an issue unchanged.
//
// LINE-based, not character-based. A build tool compares generated files,
// manifests and command output, all of which are line-oriented; a
// character-level diff of a 4000-line generated file is unreadable and costs far
// more to compute.
var Diff = Module{
	Name: "diff",
	WASM: true,
	Doc:  "Unified line diffs, for reporting what drifted rather than only that something did.",
	Methods: []Method{
		{
			Name: "unified",
			Doc:  "Return a unified diff of a and b, or \"\" when they are identical - so the result doubles as the check. from_label and to_label name the two sides in the +++/--- header (default \"a\" and \"b\"); pass the file path and something like \"regenerated\" to make a drift report read like a patch. context is the unchanged lines kept around each hunk, defaulting to 3 as git does.",
			Args: []Arg{
				{Name: "a", Type: TypeString},
				{Name: "b", Type: TypeString},
				{Name: "from_label", Type: TypeString, Optional: true},
				{Name: "to_label", Type: TypeString, Optional: true},
				{Name: "context", Type: TypeInt, Optional: true},
			},
			Returns: []Ret{{Type: TypeString}},
			Impl:    DiffUnified,
		},
		{
			Name: "equal",
			Doc:  "Report whether a and b are identical after normalizing line endings and a single trailing newline. It is the comparison a drift gate wants: a file that differs only by CRLF or by whether the last line is newline-terminated has not drifted in any sense a reader cares about, and a bare == would say it had.",
			Args: []Arg{
				{Name: "a", Type: TypeString},
				{Name: "b", Type: TypeString},
			},
			Returns: []Ret{{Type: TypeBool}},
			Impl:    DiffEqual,
		},
		{
			Name: "stat",
			Doc:  "Return a one-line summary of the change: \"3 added, 1 removed\", or \"\" when a and b are identical. For a report that wants the shape of a drift without the whole patch - a summary line per file, with unified reserved for the one the reader drills into.",
			Args: []Arg{
				{Name: "a", Type: TypeString},
				{Name: "b", Type: TypeString},
			},
			Returns: []Ret{{Type: TypeString}},
			Impl:    DiffStat,
		},
	},
}

// diffLines splits a text into lines for difflib, which wants each line to keep
// its terminator so the "\ No newline at end of file" case renders correctly.
//
// An empty input is NO lines rather than one empty line: comparing "" against a
// file must report every line as added, not report a phantom first line changed.
func diffLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.SplitAfter(s, "\n")
	// SplitAfter leaves a trailing "" when the text ends in a newline.
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// DiffUnified returns a unified diff of a and b, or "" when they are identical.
func DiffUnified(_ context.Context, a, b, fromLabel, toLabel string, contextLines int) (string, error) {
	if fromLabel == "" {
		fromLabel = "a"
	}
	if toLabel == "" {
		toLabel = "b"
	}
	if contextLines <= 0 {
		contextLines = diffDefaultContext
	}
	out, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        diffLines(a),
		B:        diffLines(b),
		FromFile: fromLabel,
		ToFile:   toLabel,
		Context:  contextLines,
	})
	if err != nil {
		return "", fmt.Errorf("diff.unified: %w", err)
	}
	return out, nil
}

// diffNormalize folds line endings and a single trailing newline, the two
// differences that are not differences for a text file.
func diffNormalize(s string) string {
	return strings.TrimSuffix(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
}

// DiffEqual reports whether a and b match once line endings and a trailing
// newline are normalized.
func DiffEqual(_ context.Context, a, b string) (bool, error) {
	return diffNormalize(a) == diffNormalize(b), nil
}

// DiffStat summarizes the change as added/removed line counts.
func DiffStat(_ context.Context, a, b string) (string, error) {
	m := difflib.NewMatcher(diffLines(a), diffLines(b))
	var added, removed int
	for _, op := range m.GetOpCodes() {
		switch op.Tag {
		case 'r': // replace: both sides move
			removed += op.I2 - op.I1
			added += op.J2 - op.J1
		case 'd':
			removed += op.I2 - op.I1
		case 'i':
			added += op.J2 - op.J1
		}
	}
	if added == 0 && removed == 0 {
		return "", nil
	}
	return fmt.Sprintf("%d added, %d removed", added, removed), nil
}
