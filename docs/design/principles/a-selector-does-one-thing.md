---
title: A selector does one thing, spelled one way
order: 39
description: Each verb picks its projects one way; a meaning has one spelling, an operand is a word that survives shell completion, and a concept that is not a path is a flag.
tags: [design, principle, cli, flags, affected, scope]
---

# A selector does one thing, spelled one way

`magus run` takes the set of projects you name. `magus affected` takes the set
the diff reaches. Neither verb takes the other's set. Nx learned this before
magus did: `nx affected` has no project list, because "affected" already means
every project the change reaches. A project list on `affected` narrows that set,
and the affected set is the gate. It exists so that nobody shrinks it, so a
selector that shrinks it is a footgun with a gate's name on it. To pick
projects, run `magus run <target> <projects>`.

Each meaning has one spelling. When a better spelling arrives, it replaces the
old one. An alias keeps two words for one meaning, and every reader, every doc
page and every guard rule then has to know both.

An operand has to survive the shell. `.` works, the way `docker build .` works:
it is a real path and it means the project at that path. `/` does not work. To
a shell it is the filesystem root: tab completion offers `/usr` and `/etc`
after it, and a reader takes it for an absolute path. "Every project" is a
selection, not a path, and a selection that is not a path is a flag, the way
kubectl spells all namespaces `-A`. So it is `--all`.

The guard that polices these selections works by cost. A run that tests less
than the change reaches is denied, because it lets a gate pass while it skips
projects: `magus run ci .` and a bare `magus run ci` from the workspace root
run only the root project. A run that tests more than the change reaches is
only advised, because it costs time and misses nothing: `magus run ci --all`.

## Enforced by

- `magus affected` refuses any project operand, `.` and `/` included, with a
  typed error that names `magus run <target> <projects>` (`affectedOperandError`
  in `cmd/magus/affected.go`, pinned by `cmd/magus/affected_test.go`).
- `magus run` refuses a `/` operand with a typed error that names `--all`
  (`rootOperandError` in `cmd/magus/run.go`, pinned by `TestRunAllFlagSuite`).
  `--all` and named projects together are refused too.
- `--all` is declared once, in the command tree (`internal/commands/registry.go`),
  so parsing, help, completions and the man page read the same flag.
- The workspace guard's ci rule (`hack/policy/cadence.buzz`) denies `run ci .`
  and a bare root `run ci`, and advises on `run ci --all`.

## Good

```sh
magus affected ci
magus run ci web libs/codec
magus run lint --all
```

The diff picks the gate's set, you pick a run's set, and every project is a
flag.

## Bad

```sh
magus affected ci web
magus run lint /
```

The first would run a gate over less than the diff reaches and still report it
green. The second spells a selection as the filesystem root, which the shell
completes into `/usr`. magus refuses both.

## See also

- [Unix survives at the verb, not the binary](unix-survives-at-the-verb.md)
- [Flags, config and help from one source](flags-config-help-one-source.md)
- [Postel's law: accept liberally, name the default](postels-law.md): liberal
  about how a value is written, never about how many words mean the same
  selection
- [Break now, hold still later](break-now-hold-still-later.md)
- [A record of refusals](../refusals.md)
