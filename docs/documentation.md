# Documentation

magus is a fast, cross-platform task orchestrator for polyglot (mono)repos. It computes the projects affected by a change, caches their results, and runs only the minimum rebuild set - all from a single, statically typed binary configured as code.

New to magus? [**Install it**](install.md), skim the two core ideas below ([Targets](targets.md) and [Spells](spells.md)), or [try it live in the playground](playground.html) without installing anything.

## Getting started

**1. [Install magus](install.md).** It is a single self-contained binary: download a build from the [releases page](https://github.com/egladman/magus/releases) and move it onto your `PATH`, or build from source. The [installation guide](install.md) covers both, plus keeping up to date.

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

- [Remote caching](remote-cache.md) - share the build cache across machines and CI, with a signing-based trust model.
- [Debugging](debugging.md) - the interactive REPL, `magus.pry()` breakpoints, and stepping through a target.
- [MCP](mcp.md) - drive magus from agents over the Model Context Protocol.
- [Telemetry](telemetry.md) - OpenTelemetry traces and metrics.

## Command-line reference

Generated man pages for every command:

- [`magus`](manpage/gen/magus.md) - the umbrella page: global flags, environment variables, and the full subcommand list.
- [`magus run`](manpage/gen/magus-run.md) - run a target; the everyday command.
- [`magus affected`](manpage/gen/magus-affected.md) - run targets only for projects a change touched, with sharding and bisection for CI.
- [`magus ls`](manpage/gen/magus-ls.md) and [`magus describe`](manpage/gen/magus-describe.md) - inspect projects, targets, and the dependency graph.
- [`magus watch`](manpage/gen/magus-watch.md) and [`magus x`](manpage/gen/magus-x.md) - re-run on change, and the interactive target picker.

## Reference

- [Standard library modules](modules/index.md) - `fs`, `os`, `http`, `json`, `crypto`, and the rest of the magusfile API.
- Diagnostic codes - [magusfile](codes/magusfile/README.md), [race](codes/race/README.md), and [sandbox](codes/sandbox/README.md) error explanations.
