//go:build !noselfupdate

package commands

import "github.com/egladman/magus/libs/clikit/command"

// selfCommand (default build) documents the `magus self` commands: update and
// install-shorthand. The update child is omitted from binaries built with
// -tags noselfupdate, so that build carries its own selfCommand.
var selfCommand = command.Command{
	Name:        "self",
	Short:       "Manage the magus binary (update, install-shorthand, place, completion, man)",
	Description: "Manage the magus binary in place, with a self-update subcommand supporting version pinning, dry-run, downgrade, and out-of-tree install directories, plus the mgs shorthand.",
	Tags:        []string{"cli", "magus self", "self update", "self install-shorthand", "self completion", "self man", "completion", "bash", "zsh", "fish", "powershell", "shell", "manpage", "documentation", "updates", "versioning", "install", "mgs"},
	Long: `Targets for managing the magus binary.

update is compiled in by default. Package maintainers who own the system
binary can build with -tags noselfupdate to disable the self-update mechanism.
install-shorthand is available in every build.

To bootstrap a workspace, use: magus init`,
	Usage: "magus self <subcommand> [flags]",
	Children: []command.Command{
		{
			Name:   "update",
			Effect: command.EffectLocalWrite,
			Short:  "Update magus to the latest release",
			Long: `Download the latest magus release from GitHub, verify its Ed25519
signature and SHA-256 hash, then atomically replace the running binary.

The release manifest (SHA256SUMS) is signed with a key embedded at build time.
Verification happens before any bytes are written to disk.

Without --bin-dir the running binary is replaced in place. With --bin-dir the
updated binary is written to <dir>/magus (or magus.exe on Windows) instead.`,
			Flags: []command.Flag{
				{Name: "check", Kind: command.Bool, Effect: command.EffectRead, Default: false, Doc: "Print whether an update is available and exit without installing"},
				{Name: "version", Kind: command.String, Doc: "Install a specific release tag (e.g. v0.4.2)"},
				{Name: "bin-dir", Kind: command.String, Doc: "Install into this directory instead of replacing in place"},
				{Name: "force", Kind: command.Bool, Default: false, Doc: "Allow downgrades and re-installs of the current version"},
				{Name: "dry-run", Kind: command.Bool, Effect: command.EffectRead, Default: false, Doc: "Verify everything but do not replace the running binary"},
				{Name: "yes", Kind: command.Bool, Default: false, Doc: "Skip interactive confirmation"},
				{Name: "y", Kind: command.Bool, Default: false, AliasOf: "yes", Doc: "Short for --yes"},
			},
			Examples: []command.Example{
				{Comment: "Update to the latest release", Command: "magus self update"},
				{Comment: "Check for an update without installing", Command: "magus self update --check"},
				{Comment: "Install a specific version", Command: "magus self update --version v0.4.2"},
				{Comment: "Non-interactive update (CI)", Command: "magus self update --yes"},
				{Comment: "Install into ~/bin instead of replacing in place", Command: "magus self update --bin-dir ~/bin"},
			},
		},
		selfInstallShorthandCommand,
		selfPlaceCommand,
		selfCompletionCommand,
		selfManCommand,
	},
	Examples: []command.Example{
		{Comment: "Update the running binary", Command: "magus self update"},
		{Comment: "Install the mgs shorthand", Command: "magus self install-shorthand"},
		{Comment: "Bash completion, re-read on every new shell", Command: `echo 'source <(magus self completion bash)' >> ~/.bashrc`},
		{Comment: "Install the man pages", Command: "magus self man"},
	},
}
