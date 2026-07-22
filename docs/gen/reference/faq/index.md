---
title: FAQ
description: Short answers to the questions that come up first with magus - spells versus targets versus charms, why runs are read-only, how the cache decides, affected builds, the daemon, and adding a tool.
tags: [faq, questions, spells, targets, charms, cache, affected, daemon]
---

# FAQ

Short answers with a link to the full story.

## What is the difference between a spell, a target, and a charm?

A **target** is a named operation on a project (`build`, `test`, `lint`). A
**spell** teaches magus how a tool performs that operation (the `go` spell knows
`go build`, `go test`). A **charm** modifies _how_ a target runs without changing
which target (`rw` to write in place, `gha` for GitHub Actions output). Target =
what, spell = how, charm = in what manner. See [targets.md](../concepts/targets.md),
[spells.md](../concepts/spells.md), [charms.md](../concepts/charms.md).

## Why did `magus run goBuild` work when my target is `go_build`?

magus normalizes every target name to canonical kebab-case on both sides: when
a magusfile declares a target and when you reference one, whether on the CLI,
in a `magus.needs` literal, or in a per-target policy key. `go_build`,
`goBuild`, and `go-build` all normalize to the same registered target, so any
spelling reaches it - there is exactly one target, not a table of aliases.
This does not apply to a spell op after `::` (`go::golangci-lint` matches
verbatim) or to a Buzz map subscript like `ts["tsc"]`. See
[targets.md](../concepts/targets.md#name-normalization-casing--delimiters).

## Why is my `format` run read-only? How do I make it write?

Every run is read-only by default, so a check never surprises you by rewriting
files. Ask for writes with the `rw` charm: `magus run format:rw`. There is no
`--write` flag; the `:rw` suffix is the one way. A workspace can opt into writing
by default with `default_charms: [rw]`. See [charms.md](../concepts/charms.md#the-rw-charm).

## How does magus decide whether to rerun a target or use the cache?

The cache is content-addressed: a target's outputs are keyed by the SHA-256 of its
declared inputs (`needs`, `provides`, `claims`). Unchanged inputs replay the
previous outputs instead of rerunning. magus caches what a target _declares_, not
what it touches, so correctness is a declaration contract. See [cache.md](../concepts/cache.md).

## How do I build only what changed?

`magus affected <target>` runs a target for every project a VCS diff touched, plus
everything downstream of those projects in the dependency graph. `magus affected
ci` is the monorepo CI workhorse. See [affected.md](../guides/affected.md).

## Do I have to run the daemon?

No. magus runs fine without it. The daemon keeps spells and services warm across
invocations and backs the MCP server; it starts on demand and is optional. Disable
it with `MAGUS_DAEMON_ENABLED=false`. See [daemon.md](../guides/daemon.md).

## How do I add support for a tool magus does not know?

Write a spell. For a one-off, a magusfile function target calling `os.exec` is
enough; for shared vocabulary, author a spell whose handler returns a `Command` (or
a `Service` for a long-running process). `magus init spell` scaffolds one. See
[spells.md](../concepts/spells.md) and the [authoring editor setup](../guides/editor.md).

## Why did two charms give me a warning about one being "overridden"?

Two active charms edited the same argument, so one silently wins (by alphabetical
name) and the other has no effect. That winner is an accident, not a decision, so
magus warns. Make the two charms edit different arguments, or drop one. See
[charms.md](../concepts/charms.md#stacking-and-composition).

## Can I run a single spell operation directly, bypassing targets?

Yes, with the `::` hatch: `magus run go::go-vet api` runs the `go` spell's `go-vet`
op in project `api`. It is an escape hatch for one-off invocation, not the everyday
surface; a target is the normal way in. See [operations.md](../concepts/operations.md).

## Is my telemetry or cache sent anywhere?

No. magus operates on your files and your infrastructure. Telemetry is off by
default and, when enabled, ships to _your_ OTLP collector, not a magus-operated
backend. The [remote cache](../concepts/remote-cache.md) is your storage. See
[telemetry.md](../concepts/telemetry.md).

## How do I see what a target will actually run before running it?

`magus describe target <path:target>` renders the fully-resolved command (charms
applied) without executing. Add `--explain` to trace each charm's edit, or a
`:charm` suffix to preview it. For a service target it also shows the readiness and
stop plan. See [charms.md](../concepts/charms.md#previewing-the-rendered-command).

## Where do I configure magus?

`magus.yaml` at the workspace root. `magus config view` prints the resolved
configuration; every key is documented in the [config reference](config.md).

## See also

- [getting-started.md](../guides/getting-started.md) - the first ten minutes.
- [conventions.md](../conventions.md) - how to read the rest of the docs.
