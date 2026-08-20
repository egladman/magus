---
title: magus self
generated_from: internal/clispec/registry.go
description: Manage the magus binary in place, with a self-update subcommand supporting version pinning, dry-run, downgrade, and out-of-tree install directories, plus the mgs shorthand.
tags: [cli, magus self, self update, self install-shorthand, updates, versioning, install, mgs]
---

# magus-self

Manage the magus binary (update, install-shorthand)

## Synopsis

**magus** self \<subcommand\> [flags]

## Description

Targets for managing the magus binary.

update is compiled in by default. Package maintainers who own the system
binary can build with -tags noselfupdate to disable the self-update mechanism.
install-shorthand is available in every build.

To bootstrap a workspace, use: magus init

### self update options

**--bin-dir** *string*
: Install into this directory instead of replacing in place

**--check**
: Print whether an update is available and exit without installing

**--dry-run**
: Verify everything but do not replace the running binary

**--force**
: Allow downgrades and re-installs of the current version

**--version** *string*
: Install a specific release tag (e.g. v0.4.2)

**-y**
: Short for --yes

**--yes**
: Skip interactive confirmation

### self install-shorthand options

**--dir** *string*
: Directory for the shorthand (default: the running binary's directory)

**--force**
: Replace an existing file at the shorthand path

## Subcommands

**update**
: Update magus to the latest release

**install-shorthand**
: Symlink mgs, the magus shorthand, to the running binary

## Examples

*Update the running binary*

```sh
magus self update
```

*Install the mgs shorthand*

```sh
magus self install-shorthand
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-memory**(1)](magus-memory.md), [**magus-notes**(1)](magus-notes.md), [**magus-diff**(1)](magus-diff.md), [**magus-server**(1)](magus-server.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-agent**(1)](magus-agent.md), [**magus-hook**(1)](magus-hook.md), [**magus-notify**(1)](magus-notify.md), [**magus-version**(1)](magus-version.md)

