---
title: magus Documentation
page_type: overview
description: The magus documentation hub covering install, targets, spells, charms, operations, engines, remote caching, MCP, telemetry, and the interactive playground.
tags: [documentation, docs, getting-started, magus, guide, index, overview]
---

# Documentation

New to magus? [Install it](download.md), skim the two core ideas below ([Targets](targets.md) and [Spells](spells.md)), or [try it live in the playground](playground.html) without installing anything.

## Philosophy

A build system sits in the hot path of development. You touch it constantly, so every small friction compounds; it earns its keep by staying out of the way.

magus does not try to define what "build", "test", or "lint" mean for your tools. That is the job of [spells](spells.md): libraries of tool-native operations. The `go` spell exposes ops like `go-build`, `go-test`, `go-vet`, `golangci-lint`, and `go-fmt`; the `rust` spell `cargo-build`, `cargo-test`, `cargo-clippy`, and `cargo-fmt`; and your magusfile composes them into the canonical targets you run (`build`, `test`, `lint`, `format`). magus handles the orchestration around them: it computes the affected projects from a change, caches their results, and runs only the minimum.

That machinery stays transparent. The cache, the daemon socket, and the run log are all files on disk; inspect them with `ls` and `cat`.

## Getting started

Prefer a linear, written walkthrough? The [Getting started guide](getting-started.md)
runs install to first `ci` pipeline as prose. The quick version:

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

- [Workspace and projects](workspace.md) - how magus discovers projects, the magusfile layout, `depends_on`, and monorepo patterns.
- [Targets](targets.md) - the named operations you run (`build`, `test`, `lint`), declared as exported functions in a magusfile.
- [Dependencies](dependencies.md) - `magus.needs` versus `depends_on`, the cross-project fold between them, and how they interact with the cache and the affected set.
- [Spells](spells.md) - language/toolchain adapters that provide tool-native operations (`go-build`, `go-test`, ...) for your targets to compose. See [Spells vs Targets](spells.md#spells-vs-targets) for where the line falls.
- [Charms](charms.md) - execution modifiers attached with `:` (for example `lint:rw` to let a read-only target write).
- [Operations and the work hierarchy](operations.md) - how a run is scheduled and parallelized across projects.
- [Cache model](cache.md) - needs/provides/claims, the content-addressed cache key, invalidation, and replay.
- [Sandbox model](sandbox.md) - the threat model and allowlist semantics that confine spell execution.
- [Services](services.md) - long-running service ops, shared one instance across dependents and invocations, with sprawl and misuse guards.
- [Wards](wards.md) - coded guardrails that reject a resolved op whose argv contradicts its kind (a detached service, a watching command).
- [Knowledge graph](knowledge.md) - the deterministic, cache-backed graph of the magus domain that `magus query`/`explain`/`path` and agents read instead of grepping.
- [Engines](engines.md) - how magus loads and evaluates a magusfile.

## Going further

Once the basics click, these cover running magus at scale and in CI.

- [CI](targets/ci.md) - compose a `ci` target with `magus.needs`, and the shared-cache trust model.
- [Daemon and concurrency](daemon.md) - one persistent process, one shared pool across every client.
- [Remote caching](remote-cache.md) - share the build cache across machines and CI, with a signing-based trust model.
- [Editor setup](editor.md) - wire your editor to `magus buzz lsp` for magusfile completion, hover, and signature help.
- [Debugging](debugging.md) - the interactive REPL, `magus.pry()` breakpoints, and stepping through a target.
- [Tips and tricks](tips.md) - non-obvious ways to combine subcommands.
- [MCP](mcp.md) - drive magus from agents over the Model Context Protocol.
- [Telemetry](telemetry.md) - OpenTelemetry traces and metrics.

## Coming from other tools

- [Coming from Nx](from-nx.md) - a terminology map and porting sketch for teams migrating a workspace from Nx.

## Reference

Generated man pages for every command:

- [`magus`](manpage/gen/magus.md) - the umbrella page: global flags, environment variables, and the full subcommand list.
- [`magus run`](manpage/gen/magus-run.md) - run a target; the everyday command.
- [`magus affected`](manpage/gen/magus-affected.md) - run targets only for projects a change touched, with sharding and bisection for CI.
- [`magus ls`](manpage/gen/magus-ls.md) and [`magus describe`](manpage/gen/magus-describe.md) - inspect projects, targets, and the dependency graph.
- [`magus watch`](manpage/gen/magus-watch.md) and [`magus x`](manpage/gen/magus-x.md) - re-run on change, and the interactive target picker.

The magusfile API and diagnostics:

- [Configuration](config.md) - every `magus.yaml` key with its `MAGUS_*` environment variable, CLI flag, and type.
- [Standard library modules](buzz/modules/index.md) - `fs`, `os`, `http`, `json`, `crypto`, and the rest of the magusfile API.
- [Spells reference](spells.md#built-in-spells) - the built-in spells (`go`, `rust`, `typescript`, `python`, `docker`, `buf`, `cosign`, `buzz`, `markdown`, `bash`), their ops, and paste-ready examples you can dry-run in place.
- Diagnostics and wards - every problem magus reports carries a stable `MGSxxxx` code with a dedicated explainer. Some are hard errors; others are [_wards_](wards.md), guardrails that flag a risky op before it runs (for example a detached service op, [MGS5002](codes/services/MGS5002.md)). Browse by family: [magusfile](codes/magusfile/README.md), [race](codes/race/README.md), [sandbox](codes/sandbox/README.md), [services](codes/services/README.md), and [knowledge graph](codes/knowledge/README.md).
- [Documentation conventions](conventions.md) - how placeholders, shell commands, runnable examples, and admonitions are written across these docs.
- [Glossary](glossary.md) - the core magus vocabulary (workspace, project, magusfile, target, spell, operation, charm, ward, module, engine) defined in one place.
