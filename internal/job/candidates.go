package job

import (
	"cmp"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/types"
)

// DeletionCandidatesLimit is how many candidates a text rendering lists before it counts
// the rest.
const DeletionCandidatesLimit = 12

const deletionHeading = "deletion candidates in the write paths: nothing else names them"

// UnreferencedInPaths is the symbols nothing references whose source file a write path of
// row covers, sorted by id. A claim (`run.go#Execute`) covers its whole file here, the way
// the guard reads it. Tests, methods and fields are left out: a test is never called, a
// method may satisfy an interface the graph cannot see, and a field is read by reflection
// as often as by name. A read-only row writes nothing and gets nil.
//
// Candidates are findings for the holder to finish or delete, never goals: the graph
// measures references, it does not judge them.
func UnreferencedInPaths(row types.Job, unreferenced []types.UnreferencedEntry) []types.UnreferencedEntry {
	if row.ReadOnly || len(row.WritePaths) == 0 {
		return nil
	}
	var out []types.UnreferencedEntry
	for _, e := range unreferenced {
		if e.Kind == "Method" || e.Kind == "Field" || knowledge.IsTestPath(e.Source) {
			continue
		}
		file, _, _ := strings.Cut(e.Source, ":")
		if slices.ContainsFunc(row.WritePaths, func(w string) bool { return covers(w, file) }) {
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(a, b types.UnreferencedEntry) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

// RenderDeletionCandidates writes candidates as the block [Terms] renders, for a command
// that prints no terms. Nothing is written for none.
func RenderDeletionCandidates(w io.Writer, candidates []types.UnreferencedEntry) {
	lines := deletionLines(candidates)
	if len(lines) == 0 {
		return
	}
	fmt.Fprintln(w, deletionHeading)
	for _, l := range lines {
		fmt.Fprintf(w, "  %s\n", l)
	}
}

// deletionLines is one line per candidate up to DeletionCandidatesLimit, then the caveat
// the measurement carries. The graph cannot see reflection, interface dispatch or a
// consumer outside the workspace, so a candidate is a place to look before deleting.
func deletionLines(candidates []types.UnreferencedEntry) []string {
	if len(candidates) == 0 {
		return nil
	}
	shown := candidates[:min(len(candidates), DeletionCandidatesLimit)]
	lines := make([]string, 0, len(shown)+2)
	for _, c := range shown {
		line := c.Label
		if c.Kind != "" {
			line += " (" + strings.ToLower(c.Kind) + ")"
		}
		if c.Source != "" {
			line += " at " + c.Source
		}
		lines = append(lines, line)
	}
	if rest := len(candidates) - len(shown); rest > 0 {
		lines = append(lines, fmt.Sprintf("and %d more; -o json lists every one", rest))
	}
	return append(lines,
		"finish each or delete it; reflection, interface dispatch and consumers outside this workspace are invisible to this list, so confirm before deleting")
}
