---
title: Shell setup
order: 7
description: Set up magus tab-completion for bash, zsh, fish, or PowerShell, and install mgs, the shorthand, as a symlink beside the binary.
tags: [completion, shell, bash, zsh, fish, powershell, mgs, shorthand, setup]
---

# Shell setup

Two things worth doing once, after magus is [installed](../download.md) and on your `PATH`: tab-completion, and the `mgs` shorthand. Both work under either name.

## Shell completion

`magus completion <shell>` prints a completion script for bash, zsh, fish, or powershell (`pwsh` is accepted too). Every script registers both `magus` and the [`mgs` shorthand](#mgs-shorthand), so you set this up once and get it under both names.

### What it completes

- **Subcommands**, and the subcommands under `graph`, `config`, `server`, `self`, and `man`.
- **Targets** after `run` and `affected`: `ls`, `build`, `test`, `lint`, `format`, `clean`, `generate`, `ci`.
- **Project paths**, live from the workspace. The script shells out to `magus ls -o name`, so it offers the projects this repo actually has rather than a baked-in list, and it stays correct as you add them.
- **Nouns** after `describe` (`spell`, `charm`, `target`, `project`, `workspace`, `module`, `mcp-tool`) and **lenses** after `insight` (`hotspots`, `affinity`, `ownership`, `trend`, `report`).
- **Flags** per subcommand, so `magus affected --<TAB>` offers `--base`, `--plan`, `--bisect`, and the rest rather than nothing.

Outside a workspace the project completions are simply empty. Nothing errors.

### bash

Source it from your rc. The script is re-read from the binary on every new shell, so it cannot go stale after an upgrade:

```sh
echo 'source <(magus completion bash)' >> ~/.bashrc
```

### zsh

Install it onto your `$fpath` as `_magus`:

```sh
mkdir -p ~/.zsh/completions
magus completion zsh > ~/.zsh/completions/_magus
```

Then in `~/.zshrc`, before `compinit` runs:

```sh
fpath=(~/.zsh/completions $fpath)
autoload -Uz compinit && compinit
```

Do **not** append the zsh script to `~/.zshrc`. It ends by invoking its own completion function, which only means something inside a completion context; sourced from an rc it runs at shell startup and registers nothing.

Unlike the bash recipe this writes a snapshot, so regenerate it after a [`magus self update`](../download.md#update) to pick up new subcommands and flags.

### fish

Drop it in the completions directory, where fish loads it on demand:

```sh
magus completion fish > ~/.config/fish/completions/magus.fish
```

### PowerShell

Append it to your profile:

```powershell
magus completion powershell >> $PROFILE
```

Full reference: [`magus completion`](../../reference/manpage/magus-completion/).

## `mgs` shorthand

The de facto shorthand for `magus` is `mgs`: three left-hand keys, fast to type, and collision-free.

The [install script](../download.md#install) creates it for you unless you pass `--no-shorthand`. To add it to an existing install:

```sh
magus self install-shorthand
```

That symlinks `mgs` next to the binary itself, so it is on your `PATH` if `magus` is. An existing `mgs` is left alone unless you pass `--force`, and `--dir` puts the link somewhere else. Full flag reference: [`magus self`](../../reference/manpage/magus-self/).

A symlink rather than a shell alias, because an alias only exists in interactive shells: `mgs` in a script, a `Makefile`, or a CI step would not resolve. The link also survives [`magus self update`](../download.md#update), which resolves symlinks before swapping the binary underneath.
