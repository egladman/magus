---
title: magus module
aliases: [modules/magus]
description: Magus core primitives.
tags: [magus, module, stdlib, magusfile]
---

# magus

Magus core primitives.

Three provider namespaces are wired by the runtime rather than declared here, so they do not appear in the method list below: `magus\cache.remote(<spell>)` selects a remote cache backend, `magus\ci.provider(<spell>)` a CI provider, and `magus\secret.provider(<spell>)` / `magus\secret.read(<ref>)` a secret backend and the credentials read through it. Each takes an imported spell handle. See [Secrets](../../concepts/secrets.md), [Remote cache](../../concepts/cache/remote.md) and [CI integration](../../guides/integrations/ci.md).

`import "magus"` resolves in a `magus buzz` script as well as in a magusfile. The members that declare into the workspace magus is loading (`magus\project`, the provider selections above) and the ones served in-process from a loaded workspace (`ls`, `targets`, `affected`, `graph`, `where`) raise [MGS1022](../codes/magusfile/MGS1022.md) in a script; the nested-command methods (`cmd`, `run`, `describe`, `insight`, `doctor`) work there and discover the workspace themselves.

> **Naming convention:** import the module under its bare name (`import "magus"`), reach members with a backslash, and call methods in `camelCase`: `magus\someMethod`.

## Methods

### cmd

Escape hatch: run `magus <sub> <args>` for a subcommand with no dedicated method (status, affected, agent, graph, ...). Its signature is the typed methods' signature with the subcommand pushed in front: magus.cmd(sub, args, [opts]) beside magus.run(args, [opts]), same argv, same opts, same ExecResult. The SUBCOMMAND is a typed argument rather than args[0] because it is the part of the invocation magus can reason about - it stays readable in the signature and greppable in the source, while the remaining argv stays free-form. Prefer the dedicated methods (run, describe, insight, doctor) when one exists - magus.cmd warns when sub names one that has. Returns {stdout, stderr, code, ok}; raises on non-zero exit (catch for non-fatal use). opts.root sets the global --root workspace; opts.dir runs it in another directory (relative to the target's, like os.exec); opts.quiet captures the output without echoing it to the console.

**Signature:** `magus\cmd(sub, args, [opts]) → ExecResult` · [source](https://github.com/egladman/magus/blob/main/std/magus.go#L290)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `sub` | `string` |  | |
| `args` | `[]string` |  | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### ls

List the workspace's projects: {workspace, count, projects}, each project {path, dir, spell, spells, sources, outputs, dependsOn, exclusive}. Annotate the result `> Projects` (magus's own type, no import needed) for compile-checked field access. Unlike magus.cmd("ls"), this reads the workspace already open on the context - no subprocess, no second workspace load, no JSON round-trip.

**Signature:** `magus\ls() → Projects` · [source](https://github.com/egladman/magus/blob/main/std/magus.go#L216)

**Returns:** map[string]any

### targets

The TARGET dependency graph of every project: {projects}, each project {path, name, engine, nodes, cycle, dependsOn} and each node {name, declared, doc, dependencies, charms, spells, crossDependencies, inputs, outputs}. Annotate the result `> TargetGraph` (magus's own type, no import needed) for compile-checked field access. This is the per-project view magus.graph() does not carry: graph() is the project-level DAG, this is the targets inside each one. Read statically from the magusfile source, so it never runs a target body, and served in-process from the workspace on the context - no subprocess, no markdown to re-parse.

**Signature:** `magus\targets() → TargetGraph` · [source](https://github.com/egladman/magus/blob/main/std/magus.go#L228)

**Returns:** map[string]any

### affected

Compute the VCS-affected project set against base (empty uses the configured base ref): {base, changed, seed, filesBySeed, affected}. Served in-process from the workspace on the context - no subprocess. Raises when the diff cannot be computed, rather than reporting an empty set, since an empty set and an uncomputable one mean opposite things to a caller deciding what to build.

**Signature:** `magus\affected([base]) → Affected` · [source](https://github.com/egladman/magus/blob/main/std/magus.go#L243)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `base` | `string` | yes | |

**Returns:** map[string]any

### graph

The project dependency DAG as {nodes, dependsOn, blastRadius}. nodes is in TOPOLOGICAL order, so iterating it is already a valid build order; dependsOn gives each node's direct predecessors and blastRadius how many projects it can transitively affect. Served in-process from the workspace on the context - no subprocess.

**Signature:** `magus\graph() → Graph` · [source](https://github.com/egladman/magus/blob/main/std/magus.go#L272)

**Returns:** map[string]any

### where

Return the project path containing dir, or null when dir is inside no project. Served in-process from the workspace on the context - no subprocess.

**Signature:** `magus\where(dir) → string` · [source](https://github.com/egladman/magus/blob/main/std/magus.go#L258)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `dir` | `string` |  | |

**Returns:** string

### run

Run `magus run <args>` recursively in the target's project directory and capture its output. Child invocations share the parent's concurrency budget over the local socket. Returns {stdout, stderr, code, ok}; raises on non-zero exit (catch for non-fatal use). opts.root sets the global --root workspace; opts.dir runs it in another directory (relative to the target's, like os.exec); opts.quiet captures the output without echoing it to the console.

**Signature:** `magus\run(args, [opts]) → ExecResult` · [source](https://github.com/egladman/magus/blob/main/std/magus.go#L307)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `args` | `[]string` |  | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### describe

Run `magus describe <args>` in the target's project directory and capture its output. Returns {stdout, stderr, code, ok}; raises on non-zero exit (catch for non-fatal use). opts.root sets the global --root workspace; opts.dir runs it in another directory (relative to the target's, like os.exec); opts.quiet captures the output without echoing it to the console. Unlike a raw binary call, the working directory is always the contextual project dir, so a nested project describes itself, not the root workspace.

**Signature:** `magus\describe(args, [opts]) → ExecResult` · [source](https://github.com/egladman/magus/blob/main/std/magus.go#L312)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `args` | `[]string` |  | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### insight

Run `magus insight <args>` in the target's project directory and capture its output. Returns {stdout, stderr, code, ok}; raises on non-zero exit (catch for non-fatal use). opts.root sets the global --root workspace; opts.dir runs it in another directory (relative to the target's, like os.exec); opts.quiet captures the output without echoing it to the console.

**Signature:** `magus\insight(args, [opts]) → ExecResult` · [source](https://github.com/egladman/magus/blob/main/std/magus.go#L317)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `args` | `[]string` |  | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### doctor

Run `magus doctor <args>` in the target's project directory and capture its output. Returns {stdout, stderr, code, ok}; raises on non-zero exit (catch for non-fatal use). opts.root sets the global --root workspace; opts.dir runs it in another directory (relative to the target's, like os.exec); opts.quiet captures the output without echoing it to the console.

**Signature:** `magus\doctor(args, [opts]) → ExecResult` · [source](https://github.com/egladman/magus/blob/main/std/magus.go#L322)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `args` | `[]string` |  | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### bustCache

Invalidate the build cache. Escape hatch - prefer modeling missing inputs as Sources. No arg clears all; a project path clears one project.

**Signature:** `magus\bustCache([project_path])` · [source](https://github.com/egladman/magus/blob/main/std/magus.go#L172)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `project_path` | `string` | yes | |

### has_charm

True when execution charm `name` is active, letting a target body branch on a charm carried in context (e.g. has_charm("rw")).

**Signature:** `magus\has_charm(name) → bool` · [source](https://github.com/egladman/magus/blob/main/std/magus.go#L165)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `name` | `string` |  | |

**Returns:** bool

