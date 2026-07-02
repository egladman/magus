//go:build !noselfupdate

package manpage

import "flag"

// selfSegment (default build) documents the `magus self` surface: update.
// It is omitted from binaries built with -tags noselfupdate, so it is part of
// the man pages only for the default build.
var selfSegment = Segment{
	Name:        "self",
	Short:       "Manage the magus binary (update)",
	Description: "Manage the magus binary in place, with a self-update subcommand supporting version pinning, dry-run, downgrade, and out-of-tree install directories.",
	Tags:        []string{"cli", "magus self", "self update", "updates", "versioning", "install"},
	Long: `Subcommands for managing the magus binary.

update is compiled in by default. Package maintainers who own the system
binary can build with -tags noselfupdate to disable the self-update mechanism.

To bootstrap a workspace, use: magus init`,
	Usage: "magus self update [flags]",
	Children: []Segment{
		{
			Name:  "update",
			Short: "Update magus to the latest release",
			Long: `Download the latest magus release from GitHub, verify its Ed25519
signature and SHA-256 hash, then atomically replace the running binary.

The release manifest (SHA256SUMS) is signed with a key embedded at build time.
Verification happens before any bytes are written to disk.

Without --bin-dir the running binary is replaced in place. With --bin-dir the
updated binary is written to <dir>/magus (or magus.exe on Windows) instead.`,
			BuildFlags: func(fs *flag.FlagSet) {
				fs.Bool("check", false, "Print whether an update is available and exit without installing")
				fs.String("version", "", "Install a specific release tag (e.g. v0.4.2)")
				fs.String("bin-dir", "", "Install into this directory instead of replacing in place")
				fs.Bool("force", false, "Allow downgrades and re-installs of the current version")
				fs.Bool("dry-run", false, "Verify everything but do not replace the running binary")
				fs.Bool("yes", false, "Skip interactive confirmation")
				fs.Bool("y", false, "Short for --yes")
			},
			Examples: []Example{
				{"Update to the latest release", "magus self update"},
				{"Check for an update without installing", "magus self update --check"},
				{"Install a specific version", "magus self update --version v0.4.2"},
				{"Non-interactive update (CI)", "magus self update --yes"},
				{"Install into ~/bin instead of replacing in place", "magus self update --bin-dir ~/bin"},
			},
		},
	},
	Examples: []Example{
		{"Update the running binary", "magus self update"},
	},
}
