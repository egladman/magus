---
title: magus x
description: Interactive shorthand for magus run with a TTY picker for project and target, remembering the last target used per project.
tags: [cli, magus x, interactive, picker, shorthand, run, tty]
---

# magus-x

Interactive shorthand: pick project + target

## Synopsis

**magus** x [filter...]

## Description

Interactive shorthand for magus run. Filters are AND-combined
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

_Browse all projects in a picker_

```sh
magus x
```

_Resolve by leaf substring_

```sh
magus x dash
```

_AND-narrow with a second filter_

```sh
magus x dash mobile
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-where**(1)](magus-where.md), [**magus-tail**(1)](magus-tail.md), [**magus-affected**(1)](magus-affected.md), [**magus-insight**(1)](magus-insight.md), [**magus-graph**(1)](magus-graph.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-server**(1)](magus-server.md), [**magus-completion**(1)](magus-completion.md), [**magus-init**(1)](magus-init.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)
