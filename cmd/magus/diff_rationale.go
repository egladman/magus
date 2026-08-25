package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/types"
)

// compatMarker is the convention's opening, INCLUDING the space the convention always
// writes after the colon. Everything from there to the closing paren is the RETIREMENT
// CONDITION, which is the half a reader needs: what would have to become true before the
// code below may go.
//
// The trailing space is load-bearing rather than cosmetic. Without it the scan matches the
// marker wherever it appears as a bare token - this file's own constant, a doc comment
// naming the convention, a lint rule's pattern - and a report whose first two hits are the
// code that produced the report is one nobody reads a third time.
const compatMarker = "compat(until: "

// rationaleHit is one deliberate decision recorded beside code this change touches.
type rationaleHit struct {
	Path string `json:"path"      yaml:"path"`
	Line int    `json:"line"      yaml:"line"`
	// Until is the retirement condition the marker declares. It is carried rather than the
	// whole comment because the condition is the part that tells a reader whether their
	// edit is the thing that retires it.
	Until string `json:"until" yaml:"until"`
}

// rationaleShown bounds the list for the same reason every other section is bounded: a
// report nobody scrolls to the end of has told the reader less than a count would.
const rationaleShown = 8

// collectRationale finds the compat(until:) markers in the files this change touches.
//
// Notes cover the decisions somebody wrote a note about, which is the small minority. This
// covers the ones recorded where they are actually kept: in a comment beside the code, under
// the marker this repository's conventions require. An audit once ranked three of its
// findings as work when each was a choice explained two lines above the thing it flagged,
// and nothing put that explanation in front of the reader at the moment they proposed to
// undo it.
//
// FILE-level, not hunk-level, and the wording says so. Deciding whether a marker sits inside
// a changed region needs the hunk ranges, and a marker fifty lines from your edit still
// governs the code you are in - claiming otherwise would be a precision this does not have.
//
// Generated files are skipped: a marker there was written by whatever produced the file.
func collectRationale(root string, rev types.Diff) []rationaleHit {
	var out []rationaleHit
	for _, f := range rev.Files {
		if f.Generated() {
			continue
		}
		out = append(out, compatMarkersIn(root, f.Path)...)
	}
	return out
}

// compatMarkersIn scans one file. An unreadable file yields nothing: a deleted path is the
// common case and is not worth a line of report.
func compatMarkersIn(root, rel string) []rationaleHit {
	fh, err := os.Open(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return nil
	}
	defer fh.Close()

	var out []rationaleHit
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for line := 1; sc.Scan(); line++ {
		idx := strings.Index(sc.Text(), compatMarker)
		if idx < 0 {
			continue
		}
		until := compatUntil(sc.Text()[idx:])
		if until == "" {
			continue
		}
		out = append(out, rationaleHit{Path: rel, Line: line, Until: until})
	}
	return out
}

// compatUntil extracts the retirement condition from a marker, empty when there is none.
//
// A condition running past the end of the line is truncated rather than dropped: these are
// prose and routinely wrap, and half a condition still tells a reader what kind of thing
// would retire the code.
func compatUntil(s string) string {
	rest := strings.TrimPrefix(s, compatMarker)
	if end := strings.Index(rest, ")"); end >= 0 {
		rest = rest[:end]
	}
	return strings.TrimSpace(rest)
}

// preflightRationaleLines renders the section, including its empty form.
func preflightRationaleLines(hits []rationaleHit) []string {
	if len(hits) == 0 {
		return []string{"RATIONALE: no compat(until:) marker in the files you changed"}
	}
	out := []string{fmt.Sprintf("RATIONALE: %d compat(until:) marker%s in files you changed - each names why the code stays",
		len(hits), pluralSuffix(len(hits), "", "s"))}
	shown := hits
	if len(shown) > rationaleShown {
		shown = shown[:rationaleShown]
	}
	for _, h := range shown {
		out = append(out, fmt.Sprintf("      %s:%d until %s", h.Path, h.Line, h.Until))
	}
	if len(hits) > len(shown) {
		out = append(out, fmt.Sprintf("      and %d more", len(hits)-len(shown)))
	}
	return out
}
