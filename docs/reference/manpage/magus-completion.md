---
title: magus completion
generated_from: internal/cli/registry.go
description: Print a bash, zsh, fish, or PowerShell completion script to stdout, ready to append to your shell startup file for tab-completion of magus commands.
tags: [cli, magus completion, completion, bash, zsh, fish, powershell, shell]
---

# magus-completion

Print a shell completion script

## Synopsis

**magus** completion \<bash|zsh|fish|powershell\>

## Description

Print a shell completion script to stdout. Every script registers both
magus and the mgs shorthand.

Completes subcommands, the targets accepted by run and affected, the nouns
accepted by describe, and each subcommand's flags. Project paths are
completed live by shelling out to magus ls -o name, so they track the
workspace instead of a baked-in list; outside a workspace they are simply
empty.

Install differs per shell, and zsh is the one that bites:

bash        source it from ~/.bashrc, which re-reads it from the binary on
              every new shell and so survives an upgrade:
                  source \<(magus completion bash)
  zsh         write it to a directory on $fpath as _magus. The script ends by
              invoking its own completion function, so sourcing it from
              ~/.zshrc runs that outside a completion context and registers
              nothing. Regenerate the file after magus self update.
  fish        write it to ~/.config/fish/completions/magus.fish, loaded on
              demand.
  powershell  append it to $PROFILE.

## Examples

*Bash: source from the rc, never goes stale*

```sh
echo 'source <(magus completion bash)' >> ~/.bashrc
```

*Zsh: install onto $fpath (not into ~/.zshrc)*

```sh
magus completion zsh > ~/.zsh/completions/_magus
```

*Fish*

```sh
magus completion fish > ~/.config/fish/completions/magus.fish
```

*PowerShell*

```sh
magus completion powershell >> $PROFILE
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-events**(1)](magus-events.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-session**(1)](magus-session.md), [**magus-memory**(1)](magus-memory.md), [**magus-notes**(1)](magus-notes.md), [**magus-diff**(1)](magus-diff.md), [**magus-server**(1)](magus-server.md), [**magus-mcp**(1)](magus-mcp.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-agent**(1)](magus-agent.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

