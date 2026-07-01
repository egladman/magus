# magus-self

Manage the magus binary (update)

## Synopsis

**magus** self update [flags]

## Description

Subcommands for managing the magus binary.

update is compiled in by default. Package maintainers who own the system
binary can build with -tags noselfupdate to disable the self-update mechanism.

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

## Subcommands

**update**
: Update magus to the latest release

## Examples

*Update the running binary*

```sh
magus self update
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-tail**(1)](magus-tail.md), [**magus-affected**(1)](magus-affected.md), [**magus-insight**(1)](magus-insight.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-server**(1)](magus-server.md), [**magus-completion**(1)](magus-completion.md), [**magus-init**(1)](magus-init.md), [**magus-version**(1)](magus-version.md)

