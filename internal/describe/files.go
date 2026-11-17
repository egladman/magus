package describe

import (
	"fmt"
	"os"
	"slices"

	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

// indexFile is the routing index a project may declare as an output.
const indexFile = "MAGUS.md"

// DeclareIndexes sets Index on each project whose declared outputs include MAGUS.md
// and returns the path to index-path map of those that do. Declarations only: an
// index that has not been generated yet still counts.
func DeclareIndexes(ws types.WorkspaceReader, projects []types.TargetGraphProject) map[string]string {
	indexes := map[string]string{}
	for i := range projects {
		p := ws.Get(projects[i].Path)
		if p == nil || !slices.ContainsFunc(p.AllOutputs(), func(g types.Glob) bool { return g.Pattern == indexFile }) {
			continue
		}
		projects[i].Index = indexFile
		indexes[projects[i].Path] = indexFile
	}
	return indexes
}

// MarkFileFocus stamps each entry with whether it is inside the focus of the
// project holding the working directory, and reports the focus it used.
//
// Computed at the render edge rather than inside ClassifyFiles, because focus is a
// fact about WHERE THE CALLER STANDS and classification is a fact about the file.
// Folding it in would give the same path two different classifications from two
// directories, and every other caller of ClassifyFiles (staging, the gate, the
// write guard) asks about the file.
//
// Silent on every uncertainty: an unresolvable cwd, or a workspace with no project
// holding it, leaves Focus empty rather than claiming everything is out.
func MarkFileFocus(ws types.WorkspaceReader, files []types.FileEntry) (project.Focus, bool) {
	cwd, err := os.Getwd()
	if err != nil {
		return project.Focus{}, false
	}
	focus, ok := project.FocusAt(ws, cwd)
	if !ok {
		return project.Focus{}, false
	}
	for i, f := range files {
		if focus.Contains(f.Path) {
			files[i].Focus = types.FocusIn
		} else {
			files[i].Focus = types.FocusOut
		}
	}
	return focus, true
}

// ClaimLabel spells a claim's declarer as the path:target ref the rest of the CLI
// takes, or the bare project for a project-wide glob no single target owns.
func ClaimLabel(c types.FileClaim) string {
	if c.Target == "" {
		return c.Project
	}
	return fmt.Sprintf("%s:%s", c.Project, c.Target)
}
