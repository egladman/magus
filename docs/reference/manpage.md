---
title: Man pages
page_type: overview
description: A curated map of the section-1 man page set - every magus subcommand's synopsis and description, grouped by what a reader is actually trying to do.
tags: [manpage, cli, man, reference, subcommands]
---

# Man pages

The complete man page set carried by the binary (`magus man install`), one
page per subcommand. Grouped here by what you are doing rather than
alphabetically.

## Running work

- [magus-run](manpage/magus-run.md) - run a named target for the selected projects.
- [magus-affected](manpage/magus-affected.md) - run a target for every project affected by a VCS diff.
- [magus-watch](manpage/magus-watch.md) - watch the workspace and emit batches of changed paths to stdout.
- [magus-x](manpage/magus-x.md) - interactive shorthand for `magus run` with a TTY picker.

## Inspecting the workspace

- [magus-ls](manpage/magus-ls.md) - list every discovered project with its language pack, sources, and outputs.
- [magus-describe](manpage/magus-describe.md) - define a magus concept and list every entity of that kind.
- [magus-where](manpage/magus-where.md) - fuzzy-match a project by leaf-anchored substring and print its path.
- [magus-graph](manpage/magus-graph.md) - the workspace's graphs as objects: deps, export, stats.
- [magus-insight](manpage/magus-insight.md) - behavioral code analysis from VCS and run-outcome history.

## Daemon and status

- [magus-server](manpage/magus-server.md) - start, stop, or check liveness of the persistent magus daemon.
- [magus-status](manpage/magus-status.md) - inspect the concurrency pool and effective configuration.
- [magus-doctor](manpage/magus-doctor.md) - run diagnostic checks against the workspace.

## Setup

- [magus-init](manpage/magus-init.md) - bootstrap a workspace with magus.yaml, magusfile.buzz, and a VCS merge driver.
- [magus-config](manpage/magus-config.md) - inspect or write configuration keys.
- [magus-completion](manpage/magus-completion.md) - print a shell completion script.
- [magus-self](manpage/magus-self.md) - manage the magus binary in place, including self-update.
- [magus-version](manpage/magus-version.md) - print the version string, commit hash, and build date.
- [magus-man](manpage/magus-man.md) - install this man page set to a chosen manpath.
- [magus](manpage/magus.md) - the root command page: what magus is and how its subcommands are organized.
