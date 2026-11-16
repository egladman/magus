package commands

import "github.com/egladman/magus/libs/clikit/command"

// selfCompletionCommand and selfManCommand live outside the build-tagged self
// pages because neither depends on the updater.
var selfCompletionCommand = command.Command{
	Name:               "completion",
	Effect:             command.EffectRead,
	UnstructuredOutput: true, // stdout is the shell script
	Short:              "Print a shell completion script",
	Long: `Print a shell completion script to stdout. Every script registers both
magus and the mgs shorthand.

Completes subcommands, the targets accepted by run and affected, the nouns
accepted by describe, and each subcommand's flags. Project paths are
completed live by shelling out to magus ls -o name, so they track the
workspace instead of a baked-in list; outside a workspace they are simply
empty.

Install differs per shell, and zsh is the one that bites:

  bash        source it from ~/.bashrc, which re-reads it from the binary on
              every new shell and so survives an upgrade:
                  source <(magus self completion bash)
  zsh         write it to a directory on $fpath as _magus. The script ends by
              invoking its own completion function, so sourcing it from
              ~/.zshrc runs that outside a completion context and registers
              nothing. Regenerate the file after magus self update.
  fish        write it to ~/.config/fish/completions/magus.fish, loaded on
              demand.
  powershell  append it to $PROFILE. pwsh is accepted as an alias, for the
              cross-platform PowerShell binary's own name.`,
	Usage: "magus self completion <bash|zsh|fish|powershell>",
	Examples: []command.Example{
		{Comment: "Bash: source from the rc, never goes stale", Command: `echo 'source <(magus self completion bash)' >> ~/.bashrc`},
		{Comment: "Zsh: install onto $fpath (not into ~/.zshrc)", Command: "magus self completion zsh > ~/.zsh/completions/_magus"},
		{Comment: "Fish", Command: "magus self completion fish > ~/.config/fish/completions/magus.fish"},
		{Comment: "PowerShell", Command: "magus self completion powershell >> $PROFILE"},
	},
}

var selfManCommand = command.Command{
	Name:   "man",
	Effect: command.EffectLocalWrite,
	Short:  "Install the section 1 man pages embedded in this binary",
	Long: `Write the complete magus manpage set carried by this binary. The installer
uses this command to place the pages under the selected installation prefix.`,
	Usage: "magus self man [--dir <path>] [--dry-run]",
	Flags: []command.Flag{
		{Name: "dir", Kind: command.String, Doc: "Directory for section 1 man pages (default: the user manpath)"},
		{Name: "dry-run", Kind: command.Bool, Effect: command.EffectRead, Default: false, Doc: "Print what would be written without touching the filesystem"},
	},
	Examples: []command.Example{
		{Comment: "Install to the default user manpath", Command: "magus self man"},
		{Comment: "Install to a custom prefix", Command: "magus self man --dir ~/.local/share/man/man1"},
	},
}
