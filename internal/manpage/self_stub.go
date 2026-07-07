//go:build noselfupdate

package manpage

// selfCommand (noselfupdate build) — update is compiled in by default and is
// omitted here because this build used -tags noselfupdate.
var selfCommand = Command{
	Name:        "self",
	Short:       "Manage the magus binary (update disabled by -tags noselfupdate)",
	Description: "Manage the magus binary in place; self-update is compiled out of this build via -tags noselfupdate, so this command is minimal.",
	Tags:        []string{"cli", "magus self", "self update", "noselfupdate"},
	Long: `Targets for managing the magus binary.

This build was compiled with -tags noselfupdate, so self update, which
downloads and replaces the binary, is not available. Rebuild without
-tags noselfupdate to enable it.

To bootstrap a workspace, use: magus init`,
	Usage:    "magus self update [flags]",
	Examples: []Example{},
}
