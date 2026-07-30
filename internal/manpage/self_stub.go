//go:build noselfupdate

package manpage

// selfCommand (noselfupdate build) - update is compiled in by default and is
// omitted here because this build used -tags noselfupdate. install-shorthand
// does not touch the updater, so it survives into this build.
var selfCommand = Command{
	Name:        "self",
	Short:       "Manage the magus binary (install-shorthand; update disabled by -tags noselfupdate)",
	Description: "Manage the magus binary in place; self-update is compiled out of this build via -tags noselfupdate, leaving install-shorthand for the mgs shorthand.",
	Tags:        []string{"cli", "magus self", "self update", "self install-shorthand", "noselfupdate", "mgs"},
	Long: `Targets for managing the magus binary.

This build was compiled with -tags noselfupdate, so self update, which
downloads and replaces the binary, is not available. Rebuild without
-tags noselfupdate to enable it. install-shorthand is unaffected.

To bootstrap a workspace, use: magus init`,
	Usage:    "magus self <subcommand> [flags]",
	Children: []Command{selfInstallShorthandCommand},
	Examples: []Example{
		{"Install the mgs shorthand", "magus self install-shorthand"},
	},
}
