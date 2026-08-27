---
title: magus x
generated_from: internal/cli/registry.go
description: Reproduces the invocation an output ref recorded, or opens a TTY picker for project and target when given filters instead.
tags: [cli, magus x, interactive, picker, shorthand, run, tty, reproduce, output ref]
---

# magus-x

Reproduce an output ref, or pick project + target

## Synopsis

**magus** x \<ref\> | magus x [filter...]

## Description

Two forms, chosen by the argument.

Given an output ref (out1a2b3c), x reproduces the invocation that minted it:
the descriptor records the project, the target and its charms, so nothing is
picked and no terminal is required. A ref copied from a CI log therefore runs
here as the same invocation - replaying from cache when the inputs match, and
running the target when they do not. Before starting it compares the recorded
cache key with the one this workspace computes and says which of the two will
happen, and it reports a dirty working tree or a differing revision rather than
implying an exactness it cannot deliver. It changes no VCS state: a differing
commit is named, never checked out.

Given filters (or nothing), x is the interactive shorthand for magus run.
Filters are AND-combined
substrings matched against project paths; ranking is leaf-anchored
longest-match-wins, so "magus x dash" prefers a project named "dashboard"
over one named "dashboards-deprecated/foo". Additional filter args narrow
the candidate set: "magus x dash mobile" requires both substrings.

When the filtered set is unique, the project picker is skipped. Otherwise
a TTY picker opens, seeded with the survivors, sorted by score. After a
project is chosen, a second picker offers the target set
(build/test/lint/format/clean/generate/ci); the last target used for that
project (persisted in $XDG_STATE_HOME/magus/x-state.json, defaulting to
$HOME/.local/state/magus/) is pre-highlighted.

x refuses to run when stdin or stderr is not a terminal: shorthand is for
humans. Scripts should call magus run directly.

## Examples

*Reproduce the invocation an output ref recorded*

```sh
magus x out1a2b3c
```

*Browse all projects in a picker*

```sh
magus x
```

*Resolve by leaf substring*

```sh
magus x dash
```

*AND-narrow with a second filter*

```sh
magus x dash mobile
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-session**(1)](magus-session.md), [**magus-memory**(1)](magus-memory.md), [**magus-notes**(1)](magus-notes.md), [**magus-diff**(1)](magus-diff.md), [**magus-server**(1)](magus-server.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-agent**(1)](magus-agent.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

