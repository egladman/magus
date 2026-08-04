---
title: Coming from Turborepo
description: A terminology map and porting sketch for a team moving a JS/TS monorepo workspace from Turborepo to magus, with an honest list of what each tool has that the other does not.
tags: [turborepo, turbo, migration, monorepo, terminology, comparison, porting]
---

# Coming from Turborepo

This page is for a team that already knows [Turborepo](https://turbo.build)
and wants to map that mental model onto magus. Both tools solve the same core
problem for a monorepo - a task graph, an affected/changed set, and a
content-addressed cache - but Turborepo is built for the JS/TS package-manager
ecosystem specifically, while magus is language-neutral. Nothing below is
written to make either tool look bad; where Turborepo has something magus does
not, that is stated plainly, and vice versa.

## Terminology map

| Turborepo                                                            | magus                                                                                                                                                                          |
| ---------------------------------------------------------------------| ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| workspace (`turbo.json` + the package manager's workspace globs)     | workspace (`magus.yaml`)                                                                                                                                                       |
| package (a directory with a `package.json`, listed in the workspace) | project (a directory whose `magusfile.buzz` registers it)                                                                                                                     |
| task (a `package.json` script name, configured in `turbo.json`)      | target (an exported `fun` in the magusfile; seven canonical names plus custom - see [targets.md](../concepts/targets.md#the-target-name))                                     |
| `tasks` (Turborepo 2) / `pipeline` (Turborepo 1) in `turbo.json`     | targets declared per project; there is no single root manifest - each project's magusfile is its own pipeline                                                                |
| the task's underlying script (`"build": "tsc -b"` in `package.json`) | the target body / a [spell](../concepts/spells.md) op                                                                                                                          |
| `dependsOn: ["^build"]` (upstream packages' `build`)                 | `ctx.needs(...)` (target-level; the `^`-upstream semantics come from a cross-project `needs` folding into `depends_on` - see [dependencies.md](../concepts/dependencies.md#the-fold-a-cross-project-needs-also-declares-depends-on)) |
| `dependsOn: ["build"]` (same-package ordering)                       | `ctx.needs(build)` calling another exported `fun` in the same magusfile                                                                                                        |
| `inputs`                                                              | a spell's `needs` globs, plus [`ctx.readsFiles(...)`](../concepts/cache.md#per-target-inputs-and-outputs) for a target-specific footprint                                     |
| `outputs`                                                             | a spell's `provides` globs, plus [`ctx.writesFiles(...)`](../concepts/cache.md#per-target-inputs-and-outputs)                                                                  |
| `"cache": false`                                                      | the `skip_cache` target policy, but only when replay would be _wrong_ (see [cache.md](../concepts/cache.md#opting-out-and-busting))                                          |
| `"persistent": true` (a dev-server task that never exits)             | a [Service](../concepts/services.md) op: readiness probes, shared-instance dedup, idle teardown - not merely a "don't cache this" flag                                        |
| `--force`                                                             | `--no-cache` (one invocation; still snapshots afterward)                                                                                                                      |
| `turbo run <task> --filter=...[ref]` (run only affected packages)    | `magus affected <target>`                                                                                                                                                     |
| `turbo run <task> --dry` / `--dry=json`                               | `--dry-run` (what one target run would do) and `magus affected --impact` (the blast radius of a changeset - see [`magus affected`](../reference/manpage/magus-affected.md))   |
| `turbo run --graph`                                                   | `magus graph` / `magus affected --graph` / `magus graph open`                                                                                                                 |
| Remote Caching (Vercel-hosted, or a self-hosted server implementing Turborepo's Remote Cache API; bearer-token auth) | [magus remote cache](../concepts/cache/remote.md) (self-hosted backends, Ed25519-signed artifacts)                                                                            |
| `turbo generate` / `turbo gen` (Plop-templated scaffolding)           | fixed, not extensible: `magus init` writes a starter magusfile, `magus init spell <name>` scaffolds one spell stub; there is no generator framework for custom, pluggable templates |
| `turbo prune` (a deployable subset workspace for one package)         | nothing - magus has no equivalent "minimal subset for a container build" packaging step                                                                                       |

## Model differences

**Config is code, not JSON.** A magusfile is [Buzz](../concepts/engines.md), a
small embedded scripting language, not a `turbo.json` document a task runner
interprets. A target is a function, and its dependencies are calls you can
trace by reading top to bottom, not a `dependsOn` array resolved by name
lookup against other tasks.

**A task name is a `package.json` script name; a target name is not tied to
any package manifest.** Turborepo's task list comes from the union of every
workspace package's `package.json` scripts - running `turbo run build` invokes
the `build` script in each package that has one, and a package with no `build`
script is silently skipped for that task. magus targets are exported functions
in a magusfile, declared once per project with no second manifest a name has
to match against.

**A bounded target vocabulary, not whatever a script happens to be named.**
Turborepo tasks are whatever string a package's scripts define - `build`,
`build:prod`, `tsc`, `compile` all coexist across packages with no shared
meaning enforced. magus has seven canonical names (`build`, `test`, `lint`,
`format`, `clean`, `generate`, `preflight`) plus `ci`, with a stated [litmus
test](../concepts/targets.md#when-does-a-name-earn-canonical-status) for
adding an eighth, so `magus run lint` means the same thing in every project.

**Read-only by default, not whatever the script does.** Every magus run is
read-only unless you add the `rw` charm (`magus run format:rw`); a Turborepo
task runs its underlying script exactly as written, so a check-vs-write
distinction (`eslint` vs `eslint --fix`) has to be two separately-named
scripts or a hand-rolled flag, with no shared, composable modifier across
tasks the way a [charm](../concepts/charms.md) is.

**Sandboxed execution.** On Linux, magus confines spell subprocesses with the
kernel's landlock LSM (see [sandbox.md](../concepts/sandbox.md)); Turborepo
has no equivalent process-level sandbox around a task's script.

**Language-neutral, no package-manager workspace required.** A magus project
is any directory whose `magusfile.buzz` registers it - a Go module, a Rust
crate, a Dockerfile-only project, or a JS package all look the same to magus.
Turborepo's project discovery is inherently tied to an npm/pnpm/yarn workspace
(`package.json` + a `workspaces` field or `pnpm-workspace.yaml`); a repository
with no JS package manager has no Turborepo workspace to speak of.

## What Turborepo has that magus does not

Said plainly, no hedging:

- **Zero-config task discovery from existing `package.json` scripts.**
  Adopting Turborepo over an existing JS/TS repo is often just adding a
  `turbo.json` around scripts that already exist, no build rewrite required.
  magus needs an actual magusfile per project.
- **`turbo prune`**, which builds a minimal deployable subset (source,
  `package.json`, lockfile) for one package's dependency closure - purpose-built
  for a slim Docker image. magus has nothing that packages a subset workspace.
- **`turbo generate`/`turbo gen`**, a Plop-backed generator framework for
  scaffolding new packages, components, or files from custom templates - magus
  has only the two fixed scaffolds in the terminology table above, not a
  pluggable generator system.
- **Native distribution via npm** (`npm install -D turbo`) and native
  integration with the JS package-manager ecosystem, including Vercel's hosted
  Remote Cache with a two-command setup (`turbo login && turbo link`). magus
  is a standalone binary you install separately; it is not npm-distributed and
  does nothing with `package.json`'s `packageManager` field.
- **`turbo watch`**, a persistent dev-loop mode that reruns affected tasks
  across the whole graph as files change.
- A large, JS/TS-specific community and first-party backing from Vercel, with
  editor and CI integrations built around that ecosystem.

Neither tool manages your language toolchain (Node version, package-manager
version, Go toolchain, ...) - that is `mise`/`volta`/`nvm`'s job either way -
but it's worth stating plainly for a team weighing the move: magus does not
pin or install toolchains, and (unlike `turbo`) it is not something you add as
an npm devDependency.

## What magus has that Turborepo does not

- A signed remote-cache trust model: every remote artifact carries a detached
  Ed25519 signature verified against a configured trust set, rather than a
  bearer-token-gated store (see [remote-cache.md](../concepts/cache/remote.md)).
- Kernel-level sandboxing of spell subprocesses on Linux (landlock).
- Services as a first-class declarative op kind, with readiness probes,
  shared-instance dedup, and idle teardown (see
  [services.md](../concepts/services.md)) - Turborepo's `persistent: true`
  only marks a task as long-running and uncacheable; it declares no readiness
  contract and dedupes nothing.
- A [knowledge graph](../concepts/knowledge.md) and MCP agent surface: `magus
  query`/`explain`/`path` let an agent (or you) navigate the project/target/spell
  domain instead of grepping across every package.json.
- [Volatility detection](../concepts/volatility.md): magus tracks and reports
  non-deterministic ("flaky") targets from run history.
- A canonical, bounded target vocabulary shared across every project,
  independent of any package's script names.
- Language-neutral single binary: no Node, npm, or JS package manager
  required, even for a workspace that has no JS in it at all.

## A porting sketch

A `turbo.json` composing a build that depends on its upstream packages'
builds, with declared inputs and outputs, plus the `package.json` scripts it
wraps:

```json
// turbo.json
{
  "$schema": "https://turbo.build/schema.json",
  "tasks": {
    "build": {
      "dependsOn": ["^build"],
      "inputs": ["src/**/*.ts", "tsconfig.json"],
      "outputs": ["dist/**"]
    },
    "test": {
      "inputs": ["src/**/*.ts", "vite.config.ts"],
      "outputs": []
    },
    "lint": {
      "outputs": []
    }
  }
}
```

```json
// package.json (per package)
{
  "scripts": {
    "build": "tsc -b",
    "test": "vitest run",
    "lint": "eslint . && tsc --noEmit"
  }
}
```

The equivalent magusfile, in the same project directory (this sketch composes
targets within one project; the upstream-package case, `dependsOn: ["^build"]`,
is a cross-project `ctx.needs(alias.target)` against an imported project handle,
which folds into `depends_on` automatically rather than needing both written by
hand - see
[Dependencies](../concepts/dependencies.md#the-fold-a-cross-project-needs-also-declares-depends-on)):

```buzz
import "magus";
import "magus/spell/typescript";

magus\project({ "spells": [typescript] });

export fun build(ctx: magus\Context, args: [str]) > void {
    typescript["tsc-build"](ctx);
}

export fun test(ctx: magus\Context, args: [str]) > void { typescript["vitest"](ctx); }

// tsc composes into lint alongside eslint - not a bespoke `typecheck` task
// with its own name to remember.
export fun lint(ctx: magus\Context, args: [str]) > void {
    typescript["tsc"](ctx);
    typescript["eslint"](ctx);
}

export fun ci(ctx: magus\Context, args: [str]) > void {
    ctx.needs(build, test, lint);
}
```

`typescript["tsc-build"]`'s `needs`/`provides` globs and `typescript["eslint"]`'s
claimed files are already declared by the spell - see the
[`typescript` spell reference](../concepts/spells/typescript.md) for the full op
list, and [Getting started](../guides/getting-started.md) for a from-scratch
walkthrough.

## See also

- [Getting started](../guides/getting-started.md): install to first `ci` pipeline, magus-native.
- [Dependencies](../concepts/dependencies.md): the `magus\needs` / `depends_on` model this page's `dependsOn` row maps to.
- [Remote caching](../concepts/cache/remote.md): the signed trust model behind the Remote Caching comparison row.
- [Services](../concepts/services.md): the declarative op kind behind the `persistent: true` comparison row.
- [Spells](../concepts/spells.md): the script/task equivalent, and the built-in spell list.
- [Coming from Nx](from-nx.md): the same porting exercise against Nx, for a team evaluating both.
