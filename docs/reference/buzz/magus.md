---
title: magus module
generated_from: reference/buzz/
aliases: [modules/magus]
description: Magus core primitives.
tags: [magus, module, stdlib, magusfile]
---

# magus

Magus core primitives.

Three provider namespaces are wired by the runtime rather than declared here, so they do not appear in the method list below: `magus\cache.remote(<spell>)` selects a remote cache provider, `magus\ci.provider(<spell>)` a CI provider, and `magus\secret.provider(<spell>)` / `magus\secret.read(<ref>)` a secret provider and the credentials read through it. Each takes an imported spell handle. `magus\secret.endpoint(<grant>)` serves the case `read` cannot: it returns a loopback base URL a CHILD PROCESS is pointed at instead of the real API, so magus attaches the credential on the way upstream and the child never holds it. It takes an object with ref/host/header/prefix fields, declared in your own magusfile. For your own code, `read` is the ordinary choice. See [Secrets](../../concepts/secrets.md), [Remote cache](../../concepts/cache/remote.md) and [CI integration](../../guides/integrations/ci.md).

`import "magus"` resolves in a `magus buzz` script as well as in a magusfile, and a script run inside a workspace reads that workspace: `projects`, `affected`, `projectGraph`, `where` and `insight` all answer in-process, and so does `magus\ledger` (list, put, register, clear): the delegation ledger an orchestrating agent declares about work it handed out (see types.Delegation). There is deliberately no `magus ledger` CLI subcommand, so this namespace and the magus_ledger MCP tool are the only doors onto it. Only the members that DECLARE into the workspace being loaded (`magus\project`, the provider selections above) raise [MGS1022](../codes/magusfile/MGS1022.md) in a script - there is nothing for them to declare into. Run a script outside any workspace and the reading members raise it too, since there is no workspace to read. The nested-command methods (`cmd`, `run`, `describe`, `doctor`) work there either way and discover the workspace themselves.

> **Naming convention:** import the module under its bare name (`import "magus"`), reach members with a backslash, and call methods in `camelCase`: `magus\someMethod`.

## Methods

### cmd

Escape hatch: run `magus <sub> <args>` for a subcommand with no dedicated method (status, affected, agent, graph, ...). Its signature is the typed methods' signature with the subcommand pushed in front: magus.cmd(sub, args, [opts]) beside magus.run(args, [opts]), same argv, same opts, same ExecResult. The SUBCOMMAND is a typed argument rather than args[0] because it is the part of the invocation magus can reason about - it stays readable in the signature and greppable in the source, while the remaining argv stays free-form. Prefer the dedicated methods (run, describe, doctor) when one exists - magus.cmd warns when sub names one that has. Returns {stdout, stderr, code, ok}; raises on non-zero exit (catch for non-fatal use). opts.root sets the global --root workspace; opts.dir runs it in another directory (relative to the target's, like proc.exec); opts.quiet captures the output without echoing it to the console.

**Signature:** `magus\cmd(sub, args, [opts]) -> ExecResult` - [source](https://github.com/egladman/magus/blob/main/std/magus.go#L730)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `sub` | `string` |  | |
| `args` | `[]string` |  | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### projects

The workspace's projects: {workspace, count, projects}, each project {path, dir, spell, spells, sources, outputs, dependsOn, exclusive}. Annotate the result `> Projects` (magus's own type, no import needed) for compile-checked field access. The sibling of magus.targets(), which answers the same question one level down. Unlike magus.cmd("ls") - the CLI spelling, which this member deliberately does not mirror - it reads the workspace already open on the context: no subprocess, no second workspace load, no JSON round-trip.

**Signature:** `magus\projects() -> Projects` - [source](https://github.com/egladman/magus/blob/main/std/magus.go#L575)

**Returns:** map[string]any

### targets

The TARGET dependency graph of every project: {projects}, each project {path, name, engine, nodes, cycle, dependsOn} and each node {name, declared, doc, dependencies, charms, spells, crossDependencies, inputs, outputs}. Annotate the result `> TargetGraph` (magus's own type, no import needed) for compile-checked field access. This is the per-project view magus.projectGraph() does not carry: that is the project-level DAG, this is the targets inside each one. Read statically from the magusfile source, so it never runs a target body. Served in-process from the workspace on the context when there is one, and through a nested magus when there is not - so the same call works from a magusfile and from a `magus buzz` script with no workspace.

**Signature:** `magus\targets([opts]) -> TargetGraph` - [source](https://github.com/egladman/magus/blob/main/std/magus.go#L594)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### affected

Compute the VCS-affected project set against base (empty uses the configured base ref): {base, changed, seed, filesBySeed, affected}. Served in-process from the workspace on the context - no subprocess. Raises when the diff cannot be computed, rather than reporting an empty set, since an empty set and an uncomputable one mean opposite things to a caller deciding what to build.

**Signature:** `magus\affected([base]) -> Affected` - [source](https://github.com/egladman/magus/blob/main/std/magus.go#L608)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `base` | `string` | yes | |

**Returns:** map[string]any

### projectGraph

The project dependency DAG as {nodes, dependsOn, blastRadius}. nodes is in TOPOLOGICAL order, so iterating it is already a valid build order; dependsOn gives each node's direct predecessors and blastRadius how many projects it can transitively affect. Served in-process from the workspace on the context - no subprocess.

**Signature:** `magus\projectGraph() -> Graph` - [source](https://github.com/egladman/magus/blob/main/std/magus.go#L712)

**Returns:** map[string]any

### where

Return the project path containing dir, or null when dir is inside no project. Served in-process from the workspace on the context - no subprocess.

**Signature:** `magus\where(dir) -> string` - [source](https://github.com/egladman/magus/blob/main/std/magus.go#L623)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `dir` | `string` |  | |

**Returns:** string

### raise

Fail with a CODED diagnostic instead of a bare string, so a caller can branch on the code: `catch (e) { if (e.code == "ACME1001") ... }`. code is yours to define and namespace - anything but the MGS prefix, which is magus's own. opts.cause is the error being wrapped, usually the value from an inner catch; it is appended to the message the way Go's %w renders one, and the failure it came from stays reachable underneath. opts.url is the page documenting the code, rendered as the `see:` line the CLI prints under its own diagnostics.

**Signature:** `magus\raise(code, message, [opts])` - [source](https://github.com/egladman/magus/blob/main/std/magus.go#L651)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `code` | `string` |  | |
| `message` | `string` |  | |
| `opts` | `map[string]any` | yes | |

### run

Run `magus run <args>` recursively in the target's project directory and capture its output. Child invocations share the parent's concurrency budget over the local socket. Returns {stdout, stderr, code, ok}; raises on non-zero exit (catch for non-fatal use). opts.root sets the global --root workspace; opts.dir runs it in another directory (relative to the target's, like proc.exec); opts.quiet captures the output without echoing it to the console.

**Signature:** `magus\run(args, [opts]) -> ExecResult` - [source](https://github.com/egladman/magus/blob/main/std/magus.go#L747)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `args` | `[]string` |  | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### describe

Run `magus describe <args>` in the target's project directory and capture its output. Returns {stdout, stderr, code, ok}; raises on non-zero exit (catch for non-fatal use). opts.root sets the global --root workspace; opts.dir runs it in another directory (relative to the target's, like proc.exec); opts.quiet captures the output without echoing it to the console. Unlike a raw binary call, the working directory is always the contextual project dir, so a nested project describes itself, not the root workspace.

**Signature:** `magus\describe(args, [opts]) -> ExecResult` - [source](https://github.com/egladman/magus/blob/main/std/magus.go#L752)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `args` | `[]string` |  | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### insight

Every VCS-history lens as one typed report: {hotspots, affinity, ownership, trend, volatility, unreferenced}. Annotate the result `> InsightReport` for compile-checked field access - `r.ownership.projects` gives each project's primary author and bus-factor flag, `r.hotspots.files` the churn-by-complexity ranking, `r.volatility` the targets that flapped. Takes the window as `{commits, since}` and renders nothing - presentation is the caller's job. Read straight off the workspace already open on the context - no subprocess, no second workspace load, no JSON round-trip. Works from a magusfile target and from a `magus buzz` script run inside a workspace; raises MGS1022 only when there is no workspace to read.

**Signature:** `magus\insight([opts]) -> InsightReport` - [source](https://github.com/egladman/magus/blob/main/std/magus.go#L792)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### affectedImpact

The VCS-affected set and WHY each project is in it: {base, changedFileCount, changedFiles, seedProjects, affectedProjects, notes}, each affected project carrying whether it was a seed and the files that pulled it in. Annotate the result `> Impact`. This is `magus affected --impact`, a forensic mode that reports the set without running a target - unlike `magus affected list`, which dispatches a target across every affected project to answer the same question. Computed in-process from the workspace on the context, so it needs a magusfile target rather than a bare `magus buzz` script. opts.commits caps the commits scanned; opts.since bounds the window (90d, 12w, 6mo, 1y).

**Signature:** `magus\affectedImpact([base], [opts]) -> Impact` - [source](https://github.com/egladman/magus/blob/main/std/magus.go#L775)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `base` | `string` | yes | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### describeFile

Classify paths against the workspace's declared globs: for each, the owning project and whether it is a declared `output` (generated - regenerate it, never hand-edit), a declared `source` (it feeds cache keys and the affected set), `maintained` (magus writes it outside any target - commit it, never ignore it), or `unclaimed`. Returns a typed DoctorReport-style envelope {definition, count, files}, not text to re-parse: this is the question "can I disregard this changed file", and a caller branches on `role` rather than grepping. Runs a nested magus, so it needs no workspace on the context and works from a `magus buzz` script.

**Signature:** `magus\describeFile(paths, [opts]) -> FileReport` - [source](https://github.com/egladman/magus/blob/main/std/magus.go#L1009)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `paths` | `[]string` |  | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### diff

Read the working tree's uncommitted changes, annotated and ordered by what they can break: for each file the owning project, whether it is a declared `output` (generated - the source edit is the review), how widely its changed symbols are referenced (`reach`), whether it is public API `surface`, observed `coverage`, how often it has been changing (`churn`), and which agent sessions wrote it (`touches`). Files come back in the order magus recommends READING them - generated last whatever its reach, then widest reach first - so a caller renders the list as given rather than sorting it again. Returns a typed Diff envelope; branch on `role` and `surface` rather than grepping text. Runs a nested magus, so it needs no workspace on the context and works from a `magus buzz` script.

**Signature:** `magus\diff([opts]) -> Diff` - [source](https://github.com/egladman/magus/blob/main/std/magus.go#L1023)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### doctor

Validate the workspace and return what every check found: {workspace, checks, summary}, each check {name, status, message, details} with status `ok`, `fail`, or `advice` (advice is worth knowing and never a gate). Annotate the result `> DoctorReport` for compile-checked field access. A caller branches on a check's status rather than grepping console text for the word fail. It does NOT raise when a check fails: doctor exits non-zero precisely when it has something to report, and raising would discard the report. Gate on `summary.fail` instead, which says more than an exit code does. It DOES raise when the underlying `magus doctor` subprocess itself cannot be launched or its output cannot be decoded - an infrastructure failure, not a check result. opts.root sets the global --root workspace; opts.dir runs it in another directory (relative to the target's, like proc.exec).

**Signature:** `magus\doctor(args, [opts]) -> DoctorReport` - [source](https://github.com/egladman/magus/blob/main/std/magus.go#L768)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `args` | `[]string` |  | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### attention

List the OPEN attention requests of this repository's session store: {requests, store}, each request {id, outcome, source, where, delegation, message, ...} as `magus session attention -o json` reports them. Read-only by design: a magusfile may refuse to proceed while a request is open, but disposing one is a human act (see the workspace doctrine's Manual-on-purpose table), so no method here closes anything - the person runs `magus session dispose <id> -reason <text>`. Runs a nested magus, so it works from a `magus buzz` script as well as a magusfile; opts.root and opts.dir as on doctor. Raises only when the subprocess cannot run or its output cannot decode.

**Signature:** `magus\attention(args, [opts]) -> map[string]any` - [source](https://github.com/egladman/magus/blob/main/std/magus.go#L759)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `args` | `[]string` |  | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### diagnoseDrift

Diagnose why a generate gate's declared outputs drifted and RETURN the verdict {drifted, code, message, url, files} so the caller decides whether to fail or warn. Pass the target's output globs and (optional) input globs, project-relative. code is MGS4006 when a declared input changed (real drift, commit it), MGS4005 when the inputs are unchanged but a dev build produced differing output (version/tool skew, not your change), or MGS4003 when a release build's identical inputs still differ (a reproducibility bug). files are the drifted outputs as Paths based at the repository root. drifted is false with every field zero when the outputs are clean. It lives here rather than on vcs because choosing between those codes is magus policy; vcs only supplies the probe. Composes vcs.status; does not replace it.

**Signature:** `magus\diagnoseDrift(outputs, [inputs]) -> DriftResult` - [source](https://github.com/egladman/magus/blob/main/std/magus.go#L1200)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `outputs` | `[]string` |  | |
| `inputs` | `[]string` | yes | |

**Returns:** any

### bustCache

Invalidate the build cache. Escape hatch - prefer modeling missing inputs as Sources. No arg clears all; a project path clears one project.

**Signature:** `magus\bustCache([project_path])` - [source](https://github.com/egladman/magus/blob/main/std/magus.go#L535)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `project_path` | `string` | yes | |

### hasCharm

True when execution charm `name` is active, letting a target body branch on a charm carried in context (e.g. has_charm("rw")).

**Signature:** `magus\hasCharm(name) -> bool` - [source](https://github.com/egladman/magus/blob/main/std/magus.go#L528)

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

### describeModule

The host modules magus exposes, with their fields, methods and rendered Buzz signatures - the records `magus describe module` prints. Omit `name` for every module; pass one to detail it.

**Signature:** `magus\describeModule([name]) -> [Module]`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `name` | `string` | yes | |

**Returns:** any

### canonicalName

The canonical form of a magus entity name - a target, charm, or spell op. `build2` gains a '-' you did not type; `HTTPServer` breaks before its last letter. Returns the NAME, never a spell handle: a handle can only come from a literal import, because the target graph is built by reading imports statically.

**Signature:** `magus\canonicalName(name) -> string`

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

