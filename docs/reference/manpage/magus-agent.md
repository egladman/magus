---
title: magus agent
generated_from: internal/cli/registry.go
description: "Render the embedded agent skills into a repository's skill directories, or print a starter AGENTS.md; it never writes the AGENTS.md you own."
tags: [cli, magus agent, skills, agents, AGENTS.md, install]
---

# magus-agent

Install the knowledge-graph agent skills into a repo

## Synopsis

**magus** agent \<install|sample\> [flags]

## Description

Render the agent skills embedded in this binary and write or stream them
into named destinations (.claude/skills, .agents/skills, .opencode/skills,
and so on).

magus never writes your AGENTS.md. That file is yours, and an installer that
edits a file you own leaves bytes you did not write and cannot audit. So
install PRINTS the managed magus block for you to paste, and only when your
AGENTS.md is missing it or is carrying a stale one. sample prints a starter
AGENTS.md to stdout for you to own and tweak, and never writes a file.

agent is a pure data generator, which is what makes --tar the general
answer: it streams a tar archive to stdout, so skills can be installed
anywhere a shell can reach. The write-to-disk form exists for the in-repo,
paths-relative-to-\<dir\> case. Absolute destinations are refused unless
--global is set, so magus cannot silently write outside the working tree.

## Options

**--dir** *string* (default: .)
: Repo directory to install into (agent install)

**--dry-run**
: Print what would be written and removed without touching the filesystem (agent install)

**--force**
: Overwrite existing installed skill files (agent install)

**--global**
: Allow absolute destination paths in write mode (agent install)

**--prune**
: Also remove installed skills this binary no longer ships; without it they are reported and left in place, and only skills magus wrote are ever candidates (agent install)

**--tar**
: Stream a tar archive to stdout instead of writing files (agent install)

## Subcommands

**install**
: Render the embedded skills and write or stream them into named destinations

**sample**
: Print a starter AGENTS.md to stdout; never writes a file

## Examples

*Install into a repo's agent skills directory*

```sh
magus agent install .claude/skills
```

*Refresh installed skills*

```sh
magus agent install .claude/skills --force
```

*Refresh, and drop skills this version no longer ships*

```sh
magus agent install .claude/skills --force --prune
```

*See what a prune would remove first*

```sh
magus agent install .claude/skills --prune --dry-run
```

*Install anywhere via tar*

```sh
magus agent install --tar | tar -xf - -C ~/.config/opencode/skills
```

*Print a starter AGENTS.md*

```sh
magus agent sample
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-session**(1)](magus-session.md), [**magus-memory**(1)](magus-memory.md), [**magus-notes**(1)](magus-notes.md), [**magus-diff**(1)](magus-diff.md), [**magus-server**(1)](magus-server.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

