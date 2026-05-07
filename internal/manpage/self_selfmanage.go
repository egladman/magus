//go:build selfmanage

package manpage

import "flag"

// selfSegment (selfmanage build) documents the full `magus self` surface:
// update and install. These exist only in binaries built with -tags selfmanage,
// so they are part of the man pages only for that build.
var selfSegment = Segment{
	Name:  "self",
	Short: "Manage the magus binary (update, install)",
	Long: `Subcommands for managing the magus binary.

update and install are compiled in only when magus is built with -tags
selfmanage (the default for official releases). Package maintainers who own the
system binary can build without that tag to disable the self-update mechanism.

To bootstrap a workspace, use: magus init`,
	Usage: "magus self <update|install> [flags]",
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
		{
			Name:  "install",
			Short: "Install magus into the user bin directory",
			Long: `Download, verify, and install a magus release into ~/.local/bin (or
--bin-dir). Unlike self update, self install targets a fresh installation
location rather than replacing the running binary, and skips the version gate
so it works correctly from a bootstrap binary.

Manpages (section 1) are written to ~/.local/share/man/man1 by default.
man-db automatically discovers this directory when ~/.local/bin is on PATH,
so man magus works with no MANPATH changes.

After installation, the PATH export line, the mgs symlink command, and the
shell completion command are printed so you can wire up the shell at your
own pace. No dotfiles are modified.`,
			BuildFlags: func(fs *flag.FlagSet) {
				fs.String("version", "", "Install a specific release tag (e.g. v0.4.2)")
				fs.String("bin-dir", "", "Install into this directory instead of ~/.local/bin")
				fs.String("man-dir", "", "Install man pages into this directory instead of ~/.local/share/man/man1")
				fs.Bool("dry-run", false, "Verify everything but do not write any files")
				fs.Bool("yes", false, "Skip interactive confirmation")
				fs.Bool("y", false, "Short for --yes")
			},
			Examples: []Example{
				{"Fresh install to ~/.local/bin", "magus self install"},
				{"Install a specific version", "magus self install --version v0.4.2"},
				{"Install into a custom directory", "magus self install --bin-dir ~/bin"},
				{"Install man pages to a custom location", "magus self install --man-dir ~/.local/share/man/man1"},
				{"Non-interactive install (CI)", "magus self install --yes"},
				{"Dry run: verify but don't write", "magus self install --dry-run"},
			},
		},
	},
	Examples: []Example{
		{"Update the running binary", "magus self update"},
		{"Fresh install to ~/.local/bin", "magus self install"},
	},
}
