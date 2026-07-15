---
title: Coming from Nx
description: A terminology map and porting sketch for a team moving a monorepo workspace from Nx to magus, with an honest list of what each tool has that the other does not.
tags: [nx, migration, monorepo, terminology, comparison, porting]
---

# Coming from Nx

This page is for a team that already knows [Nx](https://nx.dev) and wants to
map that mental model onto magus. Nx and magus solve the same core problem -
a task graph, an affected set, and a content-addressed cache for a monorepo -
with different philosophies: **Nx infers**, reading `package.json`/`project.json`
and plugin conventions to build its graph; **magus declares**, reading exactly
what a magusfile says and nothing it guesses. Nothing below is written to make
either tool look bad; where Nx has something magus does not, that is stated
plainly, and vice versa.

## Terminology map

| Nx                                             | magus                                                                 |
| ------------------------------------------------ | ------------------------------------------------------------------------ |
| workspace (`nx.json`)                            | workspace (`magus.yaml`)                                                  |
| project (`project.json` / `package.json`)        | project (a directory whose `magusfile.buzz` registers it)                |
| target (`project.json` `targets`)                | target (an exported `fun` in the magusfile; seven canonical names plus custom - see [targets.md](targets.md#the-target-name)) |
| executor / plugin                                | spell op (a [spell](spells.md) is a library of tool-native ops)           |
| `nx:run-commands`                                | `os.exec(...)` in a target body                                          |
| `dependsOn: ["^build"]`                          | `magus.needs(...)` (target-level; the `^`-upstream semantics come from `depends_on` plus same-target ordering - see [dependencies.md](dependencies.md)) |
| `implicitDependencies`                           | `depends_on` in `magus.project`                                          |
| `inputs` / `namedInputs`                         | a spell's `needs` globs, plus a project's own [`sources`](workspace.md#magusproject-layering-policy) |
| `outputs`                                        | `outputs` / a spell's `provides` globs                                   |
| `nx affected`                                    | `magus affected`                                                          |
| `nx graph`                                       | `magus graph` / `magus affected --graph` / `magus graph open`            |
| Nx Cloud remote cache (Nx Replay)                | [magus remote cache](remote-cache.md) (self-hosted backends, Ed25519-signed artifacts) |
| Nx Cloud DTE / Nx Agents                         | `magus affected --plan` (a provider-neutral JSON shard plan; you bring the runners) |
| generators / scaffolding                         | fixed, not extensible: `magus init` writes a starter magusfile, `magus init spell <name>` scaffolds one spell stub; there is no generator framework for custom, pluggable scaffolds |
| task pipeline (`targetDefaults` in `nx.json`)     | composed `magus.needs` calls in the magusfile                             |

## Model differences

**Config is code, not JSON.** A magusfile is [Buzz](engines.md), a small
embedded scripting language, not a JSON/YAML document a plugin interprets.
There is no schema to look up; a target is a function, and its dependencies
are calls you can trace by reading top to bottom.

**Explicit declarations, not plugin inference.** Nx plugins read your
`package.json`/config files and infer targets, inputs, and dependencies for
you - powerful, but the inference is only as good as the plugin's
understanding of your setup. magus caches exactly what you declare: a spell's
`needs` and a project's `depends_on` are the whole story, and under-declaring
an input is the one way to get a stale cache hit (see
[dependencies.md](dependencies.md#caching-interplay)). Nothing is inferred
from source-code analysis.

**A canonical target vocabulary, not free-form names.** Nx targets are
whatever string a plugin or `project.json` names them. magus has seven
canonical names (`build`, `test`, `lint`, `format`, `clean`, `generate`,
`preflight`) plus `ci`, with a stated [litmus test](targets.md#when-does-a-name-earn-canonical-status)
for adding an eighth - custom names are allowed, but the vocabulary is
deliberately small so `magus run lint` means the same thing in every project.

**Read-only by default, not mutate-by-default.** Every magus run is read-only
unless you add the `rw` charm (`magus run format:rw`); Nx targets run
whatever their executor does, with no equivalent default-safe mode.

**Sandboxed execution.** On Linux, magus confines spell subprocesses with the
kernel's landlock LSM (see [sandbox.md](sandbox.md)); Nx has no equivalent
process-level sandbox.

**Single static binary, no Node runtime required.** magus is a compiled Go
binary; running it does not require Node, npm, or any JS toolchain, even for a
non-JS workspace. Nx is an npm package and requires Node to run.

## What Nx has that magus does not

Said plainly, no hedging:

- A large plugin ecosystem and community, covering most popular frameworks
  out of the box.
- An extensible generator framework (`nx generate`, local generators) for
  scaffolding new projects and files with custom, pluggable templates - magus
  has only the two fixed scaffolds above, not a generator system.
- First-party editor extensions (Nx Console) with rich UI for running tasks
  and visualizing the graph.
- Nx Cloud's managed distributed task execution (Nx Agents) and a flaky-task
  retry service, as a hosted product.
- Years of production maturity across a very wide range of ecosystems.

## What magus has that Nx does not

- A signed remote-cache trust model: every remote artifact carries a
  detached Ed25519 signature verified against a configured trust set, not
  just an access-token-gated store (see [remote-cache.md](remote-cache.md)).
- Kernel-level sandboxing of spell subprocesses on Linux (landlock).
- Services as a first-class declarative op kind, with readiness probes,
  shared-instance dedup, and idle teardown (see [services.md](services.md)),
  rather than a `run-commands` invocation of a script you write yourself.
- A [knowledge graph](knowledge.md) and MCP agent surface: `magus query` /
  `explain` / `path` let an agent (or you) navigate the project/target/spell
  domain instead of grepping.
- [Volatility detection](volatility.md): magus tracks and reports
  non-deterministic ("flaky") targets from run history, distinct from a
  hosted retry service.
- Language-neutral single binary, no Node runtime dependency.

## A porting sketch

An Nx `project.json` composing a build that depends on its upstream's build,
with declared inputs and outputs:

```json
{
  "targets": {
    "build": {
      "executor": "@nx/js:tsc",
      "dependsOn": ["^build"],
      "inputs": ["{projectRoot}/src/**/*.ts", "{projectRoot}/tsconfig.json"],
      "outputs": ["{projectRoot}/dist"]
    },
    "test": {
      "executor": "@nx/vite:test",
      "inputs": ["{projectRoot}/src/**/*.ts", "{projectRoot}/vite.config.ts"]
    },
    "lint": {
      "executor": "@nx/eslint:lint"
    }
  }
}
```

The equivalent magusfile, in the same project directory - the upstream
dependency is declared once, at the target that needs it, via a project
import (see [Dependencies](dependencies.md#the-fold-a-literal-cross-project-needs-also-declares-depends_on)):

```buzz
import "magus";
import "project/../shared-lib" as shared;
import "magus/spell/ts";

magus.project({ "spells": [ts] });

export fun build(args: [str]) > void {
    magus.needs(shared.build);   // folds into depends_on automatically
    ts["tsc-build"]();
}

export fun test(args: [str]) > void { ts["vitest"](); }

// tsc composes into lint alongside eslint - not a bespoke `typecheck` target.
export fun lint(args: [str]) > void {
    ts["tsc"]();
    ts["eslint"]();
}

export fun ci(args: [str]) > void {
    magus.needs(magus.target.literal("build"), magus.target.literal("test"),
                magus.target.literal("lint"));
}
```

`ts["tsc-build"]`'s `needs`/`provides` globs and `ts["eslint"]`'s claimed
files are already declared by the spell - see the [`ts` spell reference](spells/ts.md)
for the full op list, and [Getting started](getting-started.md) for a
from-scratch walkthrough.

## See also

- [Getting started](getting-started.md): install to first `ci` pipeline, magus-native.
- [Dependencies](dependencies.md): the `magus.needs` / `depends_on` model this page's `dependsOn` row maps to.
- [Remote caching](remote-cache.md): the signed trust model behind the Nx Cloud comparison row.
- [Spells](spells.md): the executor/plugin equivalent, and the built-in spell list.
