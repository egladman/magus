---
title: magus self
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

[**magus**(1)](magus.md), [**magus-completion**(1)](magus-completion.md), [**magus-version**(1)](magus-version.md), [**magus-init**(1)](magus-init.md)

