//go:build !noselfupdate

package commands

import "github.com/egladman/magus/libs/cmdline"

// selfCommand (default build) documents the `magus self` commands: update and
// install-shorthand. The update child is omitted from binaries built with
// -tags noselfupdate, so that build carries its own selfCommand.
var selfCommand = cmdline.Command{
	Name:        "self",
	Short:       "Manage the magus binary (update, install-shorthand)",
	Description: "Manage the magus binary in place, with a self-update subcommand supporting version pinning, dry-run, downgrade, and out-of-tree install directories, plus the mgs shorthand.",
	Tags:        []string{"cli", "magus self", "self update", "self install-shorthand", "updates", "versioning", "install", "mgs"},
	Long: `Targets for managing the magus binary.

update is compiled in by default. Package maintainers who own the system
binary can build with -tags noselfupdate to disable the self-update mechanism.
install-shorthand is available in every build.

To bootstrap a workspace, use: magus init`,
	Usage: "magus self <subcommand> [flags]",
	Children: []cmdline.Command{
		{
			Name:  "update",
			Short: "Update magus to the latest release",
			Long: `Download the latest magus release from GitHub, verify its Ed25519
signature and SHA-256 hash, then atomically replace the running binary.

The release manifest (SHA256SUMS) is signed with a key embedded at build time.
Verification happens before any bytes are written to disk.

Without --bin-dir the running binary is replaced in place. With --bin-dir the
updated binary is written to <dir>/magus (or magus.exe on Windows) instead.`,
			Flags: []cmdline.Flag{
				{Name: "check", Kind: cmdline.Bool, Doc: "Print whether an update is available and exit without installing"},
				{Name: "version", Kind: cmdline.String, Doc: "Install a specific release tag (e.g. v0.4.2)"},
				{Name: "bin-dir", Kind: cmdline.String, Doc: "Install into this directory instead of replacing in place"},
				{Name: "force", Kind: cmdline.Bool, Doc: "Allow downgrades and re-installs of the current version"},
				{Name: "dry-run", Kind: cmdline.Bool, Doc: "Verify everything but do not replace the running binary"},
				{Name: "yes", Kind: cmdline.Bool, Doc: "Skip interactive confirmation"},
				{Name: "y", Kind: cmdline.Bool, AliasOf: "yes", Doc: "Short for --yes"},
			},
			Examples: []cmdline.Example{
				{"Update to the latest release", "magus self update"},
				{"Check for an update without installing", "magus self update --check"},
				{"Install a specific version", "magus self update --version v0.4.2"},
				{"Non-interactive update (CI)", "magus self update --yes"},
				{"Install into ~/bin instead of replacing in place", "magus self update --bin-dir ~/bin"},
			},
		},
		selfInstallShorthandCommand,
	},
	Examples: []cmdline.Example{
		{"Update the running binary", "magus self update"},
		{"Install the mgs shorthand", "magus self install-shorthand"},
	},
}
