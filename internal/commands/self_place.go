package commands

import "github.com/egladman/magus/libs/cmdline"

// selfPlaceCommand documents `magus self place`. It lives outside the build-tagged
// self pages because placing a binary does not depend on the updater.
var selfPlaceCommand = cmdline.Command{
	Name:  "place",
	Short: "Replace the running binary with a newer stamped build",
	Long: `Replace the running magus binary with the given one.

The replacement must report a clean commit stamp (magus version) newer than the
running build; a modified-tree or unstamped build is refused. The file is renamed
into place, so the target gets a new inode. Copying over the old file instead
leaves a stale code signature on macOS and every later exec is killed.`,
	Usage: "magus self place <binary>",
	Examples: []cmdline.Example{
		{Comment: "Replace ./magus with a fresh build", Command: "magus self place /tmp/magus-new"},
	},
}
