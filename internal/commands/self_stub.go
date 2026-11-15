//go:build noselfupdate

package commands

import "github.com/egladman/magus/libs/clikit/command"

// selfCommand (noselfupdate build) - update is compiled in by default and is
// omitted here because this build used -tags noselfupdate. install-shorthand
// does not touch the updater, so it survives into this build.
var selfCommand = command.Command{
	Name:        "self",
	Short:       "Manage the magus binary (install-shorthand, place, completion, man; update disabled by -tags noselfupdate)",
	Description: "Manage the magus binary in place; self-update is compiled out of this build via -tags noselfupdate, leaving install-shorthand for the mgs shorthand.",
	Tags:        []string{"cli", "magus self", "self update", "self install-shorthand", "self completion", "self man", "completion", "bash", "zsh", "fish", "powershell", "shell", "manpage", "documentation", "noselfupdate", "mgs"},
	Long: `Targets for managing the magus binary.

This build was compiled with -tags noselfupdate, so self update, which
downloads and replaces the binary, is not available. Rebuild without
-tags noselfupdate to enable it. install-shorthand is unaffected.

To bootstrap a workspace, use: magus init`,
	Usage:    "magus self <subcommand> [flags]",
	Children: []command.Command{selfInstallShorthandCommand, selfPlaceCommand, selfCompletionCommand, selfManCommand},
	Examples: []command.Example{
		{Comment: "Install the mgs shorthand", Command: "magus self install-shorthand"},
		{Comment: "Bash completion, re-read on every new shell", Command: `echo 'source <(magus self completion bash)' >> ~/.bashrc`},
		{Comment: "Install the man pages", Command: "magus self man"},
	},
}
