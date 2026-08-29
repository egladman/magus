//go:build noselfupdate

package cli

// selfCommand (noselfupdate build) - update is compiled in by default and is
// omitted here because this build used -tags noselfupdate. refresh, registry,
// and install-shorthand do not touch the updater, so they survive into this
// build.
var selfCommand = Command{
	Name:        "self",
	Short:       "Manage the magus binary (refresh, registry, install-shorthand; update disabled by -tags noselfupdate)",
	Description: "Manage the magus binary in place; self-update is compiled out of this build via -tags noselfupdate, leaving registry data refresh and install-shorthand for the mgs shorthand.",
	Tags:        []string{"cli", "magus self", "self update", "self refresh", "self registry", "self install-shorthand", "noselfupdate", "mgs"},
	Long: `Targets for managing the magus binary.

This build was compiled with -tags noselfupdate, so self update, which
downloads and replaces the binary, is not available. Rebuild without
-tags noselfupdate to enable it. refresh, registry, and install-shorthand
are unaffected.

To bootstrap a workspace, use: magus init`,
	Usage:    "magus self <subcommand> [flags]",
	Children: []Command{selfRefreshCommand, selfRegistryCommand, selfInstallShorthandCommand},
	Examples: []Example{
		{"Refresh the registry data", "magus self refresh"},
		{"Show cached registry state", "magus self registry"},
		{"Install the mgs shorthand", "magus self install-shorthand"},
	},
}
