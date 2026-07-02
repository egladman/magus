# magus-init

Bootstrap a workspace (magus.yaml + magusfile.tl + merge driver)

## Synopsis

****

## Description

Bootstrap a magus workspace in the current directory.

By default, magus.yaml is written to $XDG_CONFIG_HOME/magus/ (the global user
config location). Use --local to write it into the repo instead (useful for
checked-in, team-shared config). The magusfile stub and VCS merge driver are
always wired in the repo.

With --global only the global config is written; the per-clone workspace
bootstrap (magusfile stub + merge driver) is skipped.

The VCS is taken from --vcs, or chosen interactively when stdin is a terminal.

## Options

**--force**
: Overwrite an existing config file

**--global**
: Write only the global config; skip the workspace bootstrap

**--local**
: Write config into the repo (CWD) instead of $XDG_CONFIG_HOME/magus/

**--vcs** *string*
: VCS to wire the merge driver for (git|hg); prompts when omitted on a TTY

## Examples

*Bootstrap the current repo*

```sh
magus init
```

*Non-interactive (CI): pick the VCS explicitly*

```sh
magus init --vcs git
```

*Write only the global config*

```sh
magus init --global
```

*Write config into the repo instead of XDG*

```sh
magus init --local
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-tail**(1)](magus-tail.md), [**magus-affected**(1)](magus-affected.md), [**magus-insight**(1)](magus-insight.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-server**(1)](magus-server.md), [**magus-completion**(1)](magus-completion.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

