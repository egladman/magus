//go:build noselfupdate

package manpage

// selfSegment (noselfupdate build) — update is compiled in by default and is
// omitted here because this build used -tags noselfupdate.
var selfSegment = Segment{
	Name:  "self",
	Short: "Manage the magus binary (update disabled by -tags noselfupdate)",
	Long: `Subcommands for managing the magus binary.

This build was compiled with -tags noselfupdate, so self update, which
downloads and replaces the binary, is not available. Rebuild without
-tags noselfupdate to enable it.

To bootstrap a workspace, use: magus init`,
	Usage:    "magus self update [flags]",
	Examples: []Example{},
}
