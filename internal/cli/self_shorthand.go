package cli

// selfInstallShorthandCommand documents `magus self install-shorthand`. It lives
// outside the build-tagged self pages because the shorthand does not depend on
// the updater, so both the default and the -tags noselfupdate build ship it.
var selfInstallShorthandCommand = Command{
	Name:  "install-shorthand",
	Short: "Symlink mgs, the magus shorthand, to the running binary",
	Long: `Create mgs, the shorthand for magus, as a symlink to the running binary.

Without --dir the link is created in the binary's own directory, which is
already on PATH if magus is. A symlink is preferred over a shell alias because
it also resolves in scripts, Makefiles, and other non-interactive shells, and
every shipped completion script already registers mgs alongside magus.

The shorthand survives magus self update: the updater resolves symlinks before
it swaps, so the real binary is replaced underneath a link that keeps pointing
at it. An existing mgs is left alone unless --force is given.`,
	Flags: []Flag{
		{Name: "dir", Kind: FlagString, Doc: "Directory for the shorthand (default: the running binary's directory)"},
		{Name: "force", Kind: FlagBool, Doc: "Replace an existing file at the shorthand path"},
	},
	Examples: []Example{
		{"Install mgs next to magus", "magus self install-shorthand"},
		{"Install it into ~/.local/bin instead", "magus self install-shorthand --dir ~/.local/bin"},
		{"Replace an existing mgs", "magus self install-shorthand --force"},
	},
}
