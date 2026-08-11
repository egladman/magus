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

Escape hatch: run `magus <sub> <args>` for a subcommand with no dedicated method (status, affected, agent, graph, ...). Its signature is the typed methods' signature with the subcommand pushed in front: magus.cmd(sub, args, [opts]) beside magus.run(args, [opts]), same argv, same opts, same ExecResult. The SUBCOMMAND is a typed argument rather than args[0] because it is the part of the invocation magus can reason about - it stays readable in the signature and greppable in the source, while the remaining argv stays free-form. Prefer the dedicated methods (run, describe, insight, doctor) when one exists - magus.cmd warns when sub names one that has. Returns {stdout, stderr, code, ok}; raises on non-zero exit (catch for non-fatal use). opts.root sets the global --root workspace; opts.dir runs it in another directory (relative to the target's, like proc.exec); opts.quiet captures the output without echoing it to the console.

**Signature:** `magus\cmd(sub, args, [opts]) → ExecResult` · [source](https://github.com/egladman/magus/blob/main/std/magus.go#L592)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `sub` | `string` |  | |
| `args` | `[]string` |  | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### ls

List the workspace's projects: {workspace, count, projects}, each project {path, dir, spell, spells, sources, outputs, dependsOn, exclusive}. Annotate the result `> Projects` (magus's own type, no import needed) for compile-checked field access. Unlike magus.cmd("ls"), this reads the workspace already open on the context - no subprocess, no second workspace load, no JSON round-trip.

**Signature:** `magus\ls() → Projects` · [source](https://github.com/egladman/magus/blob/main/std/magus.go#L439)

**Returns:** map[string]any

### targets

The TARGET dependency graph of every project: {projects}, each project {path, name, engine, nodes, cycle, dependsOn} and each node {name, declared, doc, dependencies, charms, spells, crossDependencies, inputs, outputs}. Annotate the result `> TargetGraph` (magus's own type, no import needed) for compile-checked field access. This is the per-project view magus.graph() does not carry: graph() is the project-level DAG, this is the targets inside each one. Read statically from the magusfile source, so it never runs a target body, and served in-process from the workspace on the context - no subprocess, no markdown to re-parse.

**Signature:** `magus\targets() → TargetGraph` · [source](https://github.com/egladman/magus/blob/main/std/magus.go#L451)

**Returns:** map[string]any

### affected

Compute the VCS-affected project set against base (empty uses the configured base ref): {base, changed, seed, filesBySeed, affected}. Served in-process from the workspace on the context - no subprocess. Raises when the diff cannot be computed, rather than reporting an empty set, since an empty set and an uncomputable one mean opposite things to a caller deciding what to build.

**Signature:** `magus\affected([base]) → Affected` · [source](https://github.com/egladman/magus/blob/main/std/magus.go#L466)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `base` | `string` | yes | |

**Returns:** map[string]any

### graph

The project dependency DAG as {nodes, dependsOn, blastRadius}. nodes is in TOPOLOGICAL order, so iterating it is already a valid build order; dependsOn gives each node's direct predecessors and blastRadius how many projects it can transitively affect. Served in-process from the workspace on the context - no subprocess.

**Signature:** `magus\graph() → Graph` · [source](https://github.com/egladman/magus/blob/main/std/magus.go#L574)

**Returns:** map[string]any

### where

Return the project path containing dir, or null when dir is inside no project. Served in-process from the workspace on the context - no subprocess.

**Signature:** `magus\where(dir) → string` · [source](https://github.com/egladman/magus/blob/main/std/magus.go#L481)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `dir` | `string` |  | |

**Returns:** string

### raise

Fail with a CODED diagnostic instead of a bare string, so a caller can branch on the code: `catch (e) { if (e.code == "ACME1001") ... }`. code is yours to define and namespace - anything but the MGS prefix, which is magus's own. opts.cause is the error being wrapped, usually the value from an inner catch; it is appended to the message the way Go's %w renders one, and the failure it came from stays reachable underneath. opts.url is the page documenting the code, rendered as the `see:` line the CLI prints under its own diagnostics.

**Signature:** `magus\raise(code, message, [opts])` · [source](https://github.com/egladman/magus/blob/main/std/magus.go#L513)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `code` | `string` |  | |
| `message` | `string` |  | |
| `opts` | `map[string]any` | yes | |

### run

Run `magus run <args>` recursively in the target's project directory and capture its output. Child invocations share the parent's concurrency budget over the local socket. Returns {stdout, stderr, code, ok}; raises on non-zero exit (catch for non-fatal use). opts.root sets the global --root workspace; opts.dir runs it in another directory (relative to the target's, like proc.exec); opts.quiet captures the output without echoing it to the console.

**Signature:** `magus\run(args, [opts]) → ExecResult` · [source](https://github.com/egladman/magus/blob/main/std/magus.go#L609)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `args` | `[]string` |  | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### describe

Run `magus describe <args>` in the target's project directory and capture its output. Returns {stdout, stderr, code, ok}; raises on non-zero exit (catch for non-fatal use). opts.root sets the global --root workspace; opts.dir runs it in another directory (relative to the target's, like proc.exec); opts.quiet captures the output without echoing it to the console. Unlike a raw binary call, the working directory is always the contextual project dir, so a nested project describes itself, not the root workspace.

**Signature:** `magus\describe(args, [opts]) → ExecResult` · [source](https://github.com/egladman/magus/blob/main/std/magus.go#L614)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `args` | `[]string` |  | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### insight

Run `magus insight <args>` in the target's project directory and capture its output. Returns {stdout, stderr, code, ok}; raises on non-zero exit (catch for non-fatal use). opts.root sets the global --root workspace; opts.dir runs it in another directory (relative to the target's, like proc.exec); opts.quiet captures the output without echoing it to the console.

**Signature:** `magus\insight(args, [opts]) → ExecResult` · [source](https://github.com/egladman/magus/blob/main/std/magus.go#L619)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `args` | `[]string` |  | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### insightReport

Every VCS-history lens plus the knowledge-graph axis, as one typed report: {hotspots, affinity, ownership, trend, volatility, graphStats}. Annotate the result `> InsightReport` for compile-checked field access - `r.ownership.projects` gives each project's primary author and bus-factor flag, `r.hotspots.files` the churn-by-complexity ranking, `r.volatility` the targets that flapped. This is the same data `magus insight report` renders as Markdown, handed over as values instead of a document to scrape. Runs a nested magus, so it works from a `magus buzz` script with no workspace on the context.

**Signature:** `magus\insightReport(args, [opts]) → InsightReport` · [source](https://github.com/egladman/magus/blob/main/std/magus.go#L652)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `args` | `[]string` |  | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### affectedImpact

The VCS-affected set and WHY each project is in it: {base, changedFileCount, changedFiles, seedProjects, affectedProjects, notes}, each affected project carrying whether it was a seed and the files that pulled it in. Annotate the result `> Impact`. This is `magus affected --impact`, a forensic mode that reports the set without running a target - unlike `magus affected list`, which dispatches a target across every affected project to answer the same question. Runs a nested magus, so it works from a `magus buzz` script with no workspace on the context.

**Signature:** `magus\affectedImpact([base], [opts]) → Impact` · [source](https://github.com/egladman/magus/blob/main/std/magus.go#L635)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `base` | `string` | yes | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### targetGraph

The TARGET dependency graph of every project as a typed value: {projects}, each with its nodes and each node's declared footprint (readsFiles / writesFiles / modifiesExistingFiles). Annotate the result `> TargetGraph`. This is what magus.targets() serves in-process, reached by a nested magus instead, so it works from a `magus buzz` script with no workspace on the context - the case that matters for CI advisories reasoning about a pull request.

**Signature:** `magus\targetGraph([opts]) → TargetGraph` · [source](https://github.com/egladman/magus/blob/main/std/magus.go#L645)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### describeFile

Classify paths against the workspace's declared globs: for each, the owning project and whether it is a declared `output` (generated - regenerate it, never hand-edit), a declared `source` (it feeds cache keys and the affected set), or `unclaimed`. Returns a typed DoctorReport-style envelope {definition, count, files}, not text to re-parse: this is the question "can I disregard this changed file", and a caller branches on `role` rather than grepping. Runs a nested magus, so it needs no workspace on the context and works from a `magus buzz` script.

**Signature:** `magus\describeFile(paths, [opts]) → FileReport` · [source](https://github.com/egladman/magus/blob/main/std/magus.go#L660)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `paths` | `[]string` |  | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### doctor

Validate the workspace and return what every check found: {workspace, checks, summary}, each check {name, status, message, details} with status `ok`, `fail`, or `advice` (advice is worth knowing and never a gate). Annotate the result `> DoctorReport` for compile-checked field access. A caller branches on a check's status rather than grepping console text for the word fail. It does NOT raise when a check fails: doctor exits non-zero precisely when it has something to report, and raising would discard the report. Gate on `summary.fail` instead, which says more than an exit code does. It DOES raise when the underlying `magus doctor` subprocess itself cannot be launched or its output cannot be decoded - an infrastructure failure, not a check result. opts.root sets the global --root workspace; opts.dir runs it in another directory (relative to the target's, like proc.exec).

**Signature:** `magus\doctor(args, [opts]) → DoctorReport` · [source](https://github.com/egladman/magus/blob/main/std/magus.go#L628)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `args` | `[]string` |  | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### diagnoseDrift

Diagnose why a generate gate's declared outputs drifted and RETURN the verdict {drifted, code, message, url, files} so the caller decides whether to fail or warn. Pass the target's output globs and (optional) input globs, project-relative. code is MGS4006 when a declared input changed (real drift, commit it), MGS4005 when the inputs are unchanged but a dev build produced differing output (version/tool skew, not your change), or MGS4003 when a release build's identical inputs still differ (a reproducibility bug). files are the drifted outputs as Paths based at the repository root. drifted is false with every field zero when the outputs are clean. It lives here rather than on vcs because choosing between those codes is magus policy; vcs only supplies the probe. Composes vcs.status; does not replace it.

**Signature:** `magus\diagnoseDrift(outputs, [inputs]) → DriftResult` · [source](https://github.com/egladman/magus/blob/main/std/magus.go#L840)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `outputs` | `[]string` |  | |
| `inputs` | `[]string` | yes | |

**Returns:** any

### bustCache

Invalidate the build cache. Escape hatch - prefer modeling missing inputs as Sources. No arg clears all; a project path clears one project.

**Signature:** `magus\bustCache([project_path])` · [source](https://github.com/egladman/magus/blob/main/std/magus.go#L395)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `project_path` | `string` | yes | |

### has_charm

True when execution charm `name` is active, letting a target body branch on a charm carried in context (e.g. has_charm("rw")).

**Signature:** `magus\has_charm(name) → bool` · [source](https://github.com/egladman/magus/blob/main/std/magus.go#L388)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `name` | `string` |  | |

**Returns:** bool

### project

Declare this directory's project: its spell, sources, outputs, and options. A magusfile calls it once at top level. Raises MGS1022 in a `magus buzz` script, which has no workspace to declare into.

**Signature:** `magus\project(config, [opts])`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `config` | `any` |  | |
| `opts` | `any` | yes | |

### canonicalName

The canonical form of a magus entity name - a target, charm, or spell op. `build2` gains a '-' you did not type; `HTTPServer` breaks before its last letter. Returns the NAME, never a spell handle: a handle can only come from a literal import, because the target graph is built by reading imports statically.

**Signature:** `magus\canonicalName(name) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `name` | `string` |  | |

**Returns:** string

### fatal

Log at error level, then abort the run with exit status 1.

**Signature:** `magus\fatal([msg])`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `msg` | `string` | yes | |

### pry

Drop into an interactive REPL at this point, with the calling scope in hand. A no-op while the magusfile is only being parsed.

**Signature:** `magus\pry()`

