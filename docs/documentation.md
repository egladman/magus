---
title: magus Documentation
description: The magus documentation hub covering install, targets, spells, charms, operations, engines, remote caching, MCP, telemetry, and the interactive playground.
tags: [documentation, docs, getting-started, magus, guide, index, overview]
---

# Documentation

New to magus? [Install it](download.md), skim the two core ideas below ([Targets](targets.md) and [Spells](spells.md)), or [try it live in the playground](playground.html) without installing anything.

## Philosophy

A build system sits in the hot path of development. You touch it constantly, so every small friction compounds; it earns its keep by staying out of the way.

So magus does not try to define what "build", "test", or "lint" mean for your tools. That is the job of [spells](spells.md): libraries of tool-native operations. The `go` spell exposes `build`/`test`/`vet`/`fmt`/`lint`, the `rust` spell `build`/`test`/`clippy`/`fmt`, and your magusfile composes them into the targets you actually run. magus handles the orchestration around them: it computes the affected projects from a change, caches their results, and runs only the minimum.

And it keeps that machinery transparent. The cache, the daemon socket, and the run log are all just files on disk; inspect them with `ls` and `cat`.

## Getting started

**1. [Install magus](download.md).** A single self-contained binary. The [Download guide](download.md) covers install, verification, and updating.

**2. Initialize your workspace.** From the root of your repo:

```sh
magus init
```

This writes `magus.yaml`, stubs a starter `magusfile.buzz`, and wires the VCS merge driver.

**3. Declare targets and run them.** Targets are exported functions in `magusfile.buzz` - no registration call needed. Each one composes operations from the spells you bind.

```buzz
import "magus";
import "spells/hello";          // ./spells/hello/spell.buzz

magus.project({ "spells": [hello] });

// Each exported function becomes a runnable target.
export fun build(args: [str]) > void { hello.build(); }
export fun test(args: [str]) > void {}

// 'ci' is the conventional anchor `magus affected ci` keys off.
export fun ci(args: [str]) > void {
    magus.needs(magus.target.literal("build"), magus.target.literal("test"));
}
```

```sh
magus ls            # list registered projects and their targets
magus run build     # run a single target
magus affected ci   # run ci only for the projects your changes touched
```

## Core concepts

Start here to understand the model magus is built on.

- [Targets](targets.md) - the named operations you run (`build`, `test`, `lint`), declared as exported functions in a magusfile.
- [Spells](spells.md) - language/toolchain adapters that provide tool-native operations (`go-build`, `go-test`, ...) for your targets to compose. See [Spells vs Targets](spells.md#spells-vs-targets) for where the line falls.
- [Charms](charms.md) - execution modifiers attached with `:` (for example `lint:rw` to let a read-only target write).
- [Operations and the work hierarchy](operations.md) - how a run is scheduled and parallelized across projects.
- [Engines](engines.md) - how magus loads and evaluates a magusfile.

## Going further

Once the basics click, these cover running magus at scale and in CI.

- [CI](ci.md) - compose a `ci` target with `magus.needs`, and the shared-cache trust model.
- [Daemon and concurrency](daemon.md) - one persistent process, one shared pool across every client.
- [Remote caching](remote-cache.md) - share the build cache across machines and CI, with a signing-based trust model.
- [Debugging](debugging.md) - the interactive REPL, `magus.pry()` breakpoints, and stepping through a target.
- [Tips and tricks](tips.md) - non-obvious ways to combine subcommands.
- [MCP](mcp.md) - drive magus from agents over the Model Context Protocol.
- [Telemetry](telemetry.md) - OpenTelemetry traces and metrics.

## Reference

Generated man pages for every command:

- [`magus`](manpage/gen/magus.md) - the umbrella page: global flags, environment variables, and the full subcommand list.
- [`magus run`](manpage/gen/magus-run.md) - run a target; the everyday command.
- [`magus affected`](manpage/gen/magus-affected.md) - run targets only for projects a change touched, with sharding and bisection for CI.
- [`magus ls`](manpage/gen/magus-ls.md) and [`magus describe`](manpage/gen/magus-describe.md) - inspect projects, targets, and the dependency graph.
- [`magus watch`](manpage/gen/magus-watch.md) and [`magus x`](manpage/gen/magus-x.md) - re-run on change, and the interactive target picker.

The magusfile API and diagnostics:

- [Standard library modules](modules/index.md) - `fs`, `os`, `http`, `json`, `crypto`, and the rest of the magusfile API.
- Diagnostic codes - [magusfile](codes/magusfile/README.md), [race](codes/race/README.md), and [sandbox](codes/sandbox/README.md) error explanations.
