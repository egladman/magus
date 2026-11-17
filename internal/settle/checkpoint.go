package settle

import (
	"fmt"

	typesvcs "github.com/egladman/magus/types/vcs"
)

// CheckpointLine is the human reading: "<rev> <branch> clean" or "<rev> <branch> dirty
// <digest>", with " preserved <handle>" appended when --preserve minted one. The field
// count is fixed through the dirty word, so a branchless revision (a detached head, jj's
// usual anonymous change) renders "-" rather than collapsing the column and silently
// shifting everything after it.
//
// The handle is on this line because --preserve promises to print one and this is the
// default rendering. Without it the two spellings of the command produced identical
// output while one of them left an object in the user's repository, and the only way back
// to that object was a format flag nobody was told to pass.
func CheckpointLine(cp typesvcs.Checkpoint) string {
	branch := cp.Branch
	if branch == "" {
		branch = "-"
	}
	if !cp.Dirty {
		return fmt.Sprintf("%s %s clean", cp.Revision, branch)
	}
	line := fmt.Sprintf("%s %s dirty %s", cp.Revision, branch, cp.PatchDigest)
	if cp.Preserved != "" {
		line += " preserved " + cp.Preserved
	}
	return line
}
