---
title: magus module
generated_from: reference/buzz/
aliases: [modules/magus]
description: Magus core primitives.
tags: [magus, module, stdlib, magusfile]
---

# magus

Magus core primitives.

Provider namespaces are wired by the runtime rather than declared here, so they do not appear in the method list below: `magus\cache.remote(<spell>)` selects a remote cache provider, `magus\ci.provider(<spell>)` a CI provider, `magus\secret.provider(<spell>)` / `magus\secret.read(<ref>)` a secret provider and the credentials read through it, `magus\harness.provider(<spell>)` an agent-host harness (many hosts; like workspace.provider, unlike cache.remote's one), and `magus\guard.shell(<rule>)` an additive shell-guard rule (strengthen-only), and `magus\guard.spawn(<fun>)` the one function the agent guard calls on every spawn and continuation (see [magus\guard.spawn](../guard-spawn.md)), and `magus\guard.command(<fun>)` the one function it calls on every agent shell command (see [magus\guard.command](../guard-command.md)), and `magus\guard.write(<fun>)` the one function it calls on every agent file write. Each provider takes an imported spell handle. `magus\secret.endpoint(<grant>)` serves the case `read` cannot: it returns a loopback base URL a CHILD PROCESS is pointed at instead of the real API, so magus attaches the credential on the way upstream and the child never holds it. It takes an object with ref/host/header/prefix fields, declared in your own magusfile. For your own code, `read` is the ordinary choice. See [Secrets](../../concepts/secrets.md), [Remote cache](../../concepts/cache/remote.md) and [CI integration](../../guides/integrations/ci.md).

`import "magus"` is how you reach any of this. The namespace is an ordinary host module, like `fs` or `vcs`: without the import line `magus` is undefined, and the import is what attaches these signatures to your call sites. It resolves in a `magus buzz` script as well as in a magusfile, and a script run inside a workspace reads that workspace. The reads hang off six root constructors, each returning a record whose methods ask about it: `magus\workspace()` (the job store, run outcomes, skills, disk), `magus\graph()` (the knowledge graph at one symbol index), `magus\change(base)` (one change against one tree), `magus\history(opts)` (one window of history), `magus\job(id)` (one row) and `magus\session(opts)` (one session's guard record). They, `describe.project`, `where` and `output` answer in-process. The `magus job` CLI subcommand is another write door onto the same job rows: ls and describe read, fork declares a row, exec records a worker's landed base, and wait blocks on a dependency. Only the members that DECLARE into the workspace being loaded (`magus\project`, the provider selections above) raise [MGS1022](../codes/magusfile/MGS1022.md) in a script: there is nothing for them to declare into. Run a script outside any workspace and the reading members raise it too, since there is no workspace to read. The nested-command methods (`cmd`, `run`, `doctor`, and the `magus\describe` methods that fork) work there either way and discover the workspace themselves.

> **Naming convention:** import the module under its bare name (`import "magus"`), reach members with a backslash, and call methods in `camelCase`: `magus\someMethod`.

## Methods

### cmd

Escape hatch: run `magus <sub> <args>` for a subcommand with no dedicated method (status, affected, agent, graph, ...). Its signature is the typed methods' signature with the subcommand pushed in front: magus\cmd(sub, args, [opts]) beside magus\run(args, [opts]), same argv, same opts, same ExecResult. The SUBCOMMAND is a typed argument rather than args[0] because it is the part of the invocation magus can reason about - it stays readable in the signature and greppable in the source, while the remaining argv stays free-form. Prefer the dedicated methods (run, doctor, the magus\describe methods) when one exists - magus\cmd warns when sub, or a describe noun, names one that has; a describe noun with no method (job), and any noun's text in an explicit -o format, is reached here without a warning. Returns {stdout, stderr, code, ok}; raises on non-zero exit unless opts.AllowFailure is set. opts.root sets the global --root workspace; opts.dir runs it in another directory (relative to the target's, like proc\exec); opts.quiet captures the output without echoing it to the console; opts.stdin feeds the child's standard input, which is how a credential reaches a subcommand (`graph push`) without passing through a process listing or a run log.

**Signature:** `magus\cmd(sub, args, [opts]) -> ExecResult` - [source](https://github.com/egladman/magus/blob/main/std/magus.go)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `sub`     | `string`         |          |             |
| `args`    | `[]string`       |          |             |
| `opts`    | `map[string]any` | yes      |             |

**Returns:** map[string]any

### where

Return the project path containing dir, or null when dir is inside no project. Served in-process from the workspace on the context - no subprocess.

**Signature:** `magus\where(dir) -> string` - [source](https://github.com/egladman/magus/blob/main/std/magus.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `dir`     | `string` |          |             |

**Returns:** string

### raise

Fail with a CODED diagnostic instead of a bare string, so a caller can branch on the code: `catch (e) { if (e.code == "WS1001") ... }`. code is a DiagnosticCode from magus\diagnosticCode, and it is checked again here against the prefix magus.yaml's diagnostics.prefix declares, so a raise from an imported spell carries the importing workspace's prefix or fails. opts.cause is the error being wrapped, usually the value from an inner catch; it is appended to the message the way Go's %w renders one, and the failure it came from stays reachable underneath. The `see:` line under the message is opts.url when given, else the page diagnostics.docs names for the code, else none.

**Signature:** `magus\raise(code, message, [opts])` - [source](https://github.com/egladman/magus/blob/main/std/magus.go)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `code`    | `map[string]any` |          |             |
| `message` | `string`         |          |             |
| `opts`    | `map[string]any` | yes      |             |

### diagnosticCode

Mint a workspace diagnostic code for magus\raise: {code, origin}. text is the prefix magus.yaml's diagnostics.prefix declares followed by four digits (WS1003). Raises on an MGS, BZZ or other built-in prefix, which only magus mints, on a prefix this workspace did not declare, on a malformed code, and on any code when no prefix is declared, naming the magus.yaml key.

**Signature:** `magus\diagnosticCode(text) -> DiagnosticCode` - [source](https://github.com/egladman/magus/blob/main/std/magus.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `text`    | `string` |          |             |

**Returns:** map[string]any

### run

Run `magus run <args>` recursively in the target's project directory and capture its output. Child invocations share the parent's concurrency budget over the local socket. Returns {stdout, stderr, code, ok}; raises on non-zero exit unless opts.AllowFailure is set. opts.root sets the global --root workspace; opts.dir runs it in another directory (relative to the target's, like proc\exec); opts.quiet captures the output without echoing it to the console; opts.stdin feeds the child's standard input.

**Signature:** `magus\run(args, [opts]) -> ExecResult` - [source](https://github.com/egladman/magus/blob/main/std/magus.go)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `args`    | `[]string`       |          |             |
| `opts`    | `map[string]any` | yes      |             |

**Returns:** map[string]any

### output

One target run's captured output by its ref: {ref, project, target, failed, durationMs, output}. Annotate the result `> OutputRecord`. ref is an output ref (out1a2b3c) or a unique prefix of one. Raises on a value that is not a ref, a prefix that matches several, and a ref this checkout's output store does not hold: output lives in the checkout that ran the target. Read in-process from the workspace on the context, or inside a guard rule from the checkout the guard judges; raises MGS1022 outside both.

**Signature:** `magus\output(ref) -> OutputRecord` - [source](https://github.com/egladman/magus/blob/main/std/magus_graph.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `ref`     | `string` |          |             |

**Returns:** map[string]any

### doctor

Validate the workspace and return what every check found: {workspace, checks, summary}, each check {name, status, message, details} with status `ok`, `fail`, or `advice` (advice is worth knowing and never a gate). Annotate the result `> DoctorReport` for compile-checked field access. A caller branches on a check's status rather than grepping console text for the word fail. It does NOT raise when a check fails: doctor exits non-zero precisely when it has something to report, and raising would discard the report. Gate on `summary.fail` instead, which says more than an exit code does. It DOES raise when the underlying `magus doctor` subprocess itself cannot be launched or its output cannot be decoded - an infrastructure failure, not a check result. opts.root sets the global --root workspace; opts.dir runs it in another directory (relative to the target's, like proc\exec).

**Signature:** `magus\doctor(args, [opts]) -> DoctorReport` - [source](https://github.com/egladman/magus/blob/main/std/magus.go)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `args`    | `[]string`       |          |             |
| `opts`    | `map[string]any` | yes      |             |

**Returns:** map[string]any

### clean

Remove the declared outputs of the selected projects: {removed, tracked, dryRun}. Arguments are `magus clean`'s: project paths, `--cache`, `--dry-run`. With no projects, the cwd project is selected, or the whole workspace from the root. Tracked outputs stay, because they are committed. Annotate the result `> CleanReport`. opts.root and opts.dir as on doctor.

**Signature:** `magus\clean(args, [opts]) -> CleanReport` - [source](https://github.com/egladman/magus/blob/main/std/magus.go)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `args`    | `[]string`       |          |             |
| `opts`    | `map[string]any` | yes      |             |

**Returns:** map[string]any

### hasCharm

True when execution charm `name` is active, letting a target body branch on a charm carried in context (e.g. has_charm("rw")).

**Signature:** `magus\hasCharm(name) -> bool` - [source](https://github.com/egladman/magus/blob/main/std/magus.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `name`    | `string` |          |             |

**Returns:** bool

### workspace

The workspace this call reads, as a Workspace whose methods are the job store, the run-outcome reads, the skills, disk reclaim and drift diagnosis. dir names the checkout whose branch forkFacts and unmerged read; empty is the workspace root. Raises MGS1022 outside a workspace.

**Signature:** `magus\workspace([dir]) -> Workspace` - [source](https://github.com/egladman/magus/blob/main/std/magus_handle.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `dir`     | `string` | yes      |             |

**Returns:** map[string]any

### graph

The knowledge graph at its current symbol index: {digest, indexed, projects, gaps}, digest a hex SHA-256 over each loaded project's symbol shard fingerprint. Its methods (query, explain, path, refs, stats, neighborhood, dir, dirs, layer, projectGraph, imports, symbols, precedents, unreferenced, duplication) answer from that index and raise once it moved, naming the projects whose index came or went. A target writes the Graph as a declared output that a reader of the index keys its cache on. indexed is false and digest empty when no index was ingested; gaps are the projects declaring an index magus could not read. Raises MGS1022 outside a workspace, and when the gap probe cannot run. languages names the languages whose symbol indexes to freshen; empty is every index.

**Signature:** `magus\graph([languages]) -> Graph` - [source](https://github.com/egladman/magus/blob/main/std/magus_handle.go)

| Parameter   | Type       | Optional | Description |
| ----------- | ---------- | -------- | ----------- |
| `languages` | `[]string` | yes      |             |

**Returns:** map[string]any

### change

A change against base, as a Change whose methods (tier, diff, impact, affected, conformance, mergeBase) read that change. base "" is the configured base, as `magus affected` and `magus diff` read it. opts.rev reviews a committed range base...head and opts.patch a unified diff instead of the working tree. The Change records the tree it was built on (at, the checkpoint, and files, a digest of each dirty path), and each method raises once that tree moved, naming the paths that moved. Raises MGS1022 outside a workspace.

**Signature:** `magus\change([base], [opts]) -> Change` - [source](https://github.com/egladman/magus/blob/main/std/magus_handle.go)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `base`    | `string`         | yes      |             |
| `opts`    | `map[string]any` | yes      |             |

**Returns:** map[string]any

### history

The window of version-control history a metric reads, as a History whose methods are hotspots, affinity, ownership and trend. opts is a HistoryMetricsOptions: commits (default 500), since, dir, and files for file granularity. Raises MGS1022 outside a workspace.

**Signature:** `magus\history([opts]) -> History` - [source](https://github.com/egladman/magus/blob/main/std/magus_handle.go)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `opts`    | `map[string]any` | yes      |             |

**Returns:** map[string]any

### job

One row of the declared job store, as a Job whose methods are exit, wait and register. The store is what an orchestrating agent said about work it handed out: rows are DECLARATIONS, gating no run; the agent guard reads them to grade a write. An id no row carries raises. Raises MGS1022 outside a workspace.

**Signature:** `magus\job(id) -> Job` - [source](https://github.com/egladman/magus/blob/main/std/magus_handle.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `id`      | `string` |          |             |

**Returns:** map[string]any

### session

Which session's guard record a read covers, as a Session whose methods are trail, marks and mark. opts.session is the host's session id, empty for the one with the newest observation; opts.since is a duration back from opts.until (`6h`) or an RFC3339 time, default 24h; opts.until is an RFC3339 time, default now. An unknown option raises, and MGS1022 outside a workspace.

**Signature:** `magus\session([opts]) -> Session` - [source](https://github.com/egladman/magus/blob/main/std/magus_handle.go)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `opts`    | `map[string]any` | yes      |             |

**Returns:** map[string]any

### project

Declare this directory's project: its spell, sources, outputs, and options. A magusfile calls it once at top level. Raises MGS1022 in a `magus buzz` script, which has no workspace to declare into.

**Signature:** `magus\project(config, [opts])`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `config`  | `any` |          |             |
| `opts`    | `any` | yes      |             |

### canonicalName

The canonical form of a magus entity name - a target, charm, or spell op. `build2` gains a '-' you did not type; `HTTPServer` breaks before its last letter. Returns the NAME, never a spell handle: a handle can only come from a literal import, because the target graph is built by reading imports statically.

**Signature:** `magus\canonicalName(name) -> string`

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `name`    | `string` |          |             |

**Returns:** string

### fatal

Log at error level, then abort the run with exit status 1.

**Signature:** `magus\fatal([msg])`

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `msg`     | `string` | yes      |             |

### pry

Drop into an interactive REPL at this point, with the calling scope in hand. A no-op while the magusfile is only being parsed.

**Signature:** `magus\pry()`

### workspaceProvider

Select the workspace provider, given an imported spell handle. Declared at the top level of the root magusfile.

**Signature:** `magus\workspaceProvider(spell)`

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `spell`   | `map[string]any` |          |             |

### modelTiers

Declare the model tiers a fork assigns: an enum of your own, cheapest case first, which magus reads once at load. tiers is that enum and default one of its cases. hosts maps each harness the magusfile wired, keyed by the harness id magus\harness.provider returns, to a function from a tier to the model alias that harness spawns; bounds maps a tier to its magus\agent\ModelTierBounds (maxLive, maxContext; a zero field is no cap); skills maps a tier to the magus\agent\SkillVariant its workers load. Write each function as an exhaustive match over the enum, so a tier added later fails `magus check` until every function covers it. No model name is magus's: the aliases are yours. hosts, bounds and skills are checked at load against the enum, and the declaration must follow the magus\harness.provider calls it maps. A declaration magus cannot use is MGS1052, and so is a second call or one outside the root magusfile. magus\modelTiers(null) turns tiers off. With no call the workspace gets the magus/modeltiers module: routine, bounded, design and frontier. Adjacent tiers may share a host's alias, for a host with fewer models than tiers. This declares; it never selects a model for a call, retries or escalates, and prints nothing. The recorded outcomes per tier are magus\workspace().tierOutcomes().

**Signature:** `magus\modelTiers(tiers, [default], [hosts], [bounds], [skills])`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `tiers`   | `any` |          |             |
| `default` | `any` | yes      |             |
| `hosts`   | `any` | yes      |             |
| `bounds`  | `any` | yes      |             |
| `skills`  | `any` | yes      |             |

### modelTier

Register the one function a fork calls to choose a job's model tier: fun(req: ModelTierRequest) > any, returning null to leave the declared default to decide, else an object with tier (a case of the modelTiers enum), skills (a SkillVariant) and a non-empty reason. It runs once per fork, never during a run, and a row that names its own tier or model bypasses it. The job store is read-only inside it: every job member raises, so it decides from the request and the recorded history alone. A non-null answer lacking tier, skills or a reason fails the fork. Declared at the top level of the root magusfile, once; a second call, a call from another project, or a non-function is MGS1052.

**Signature:** `magus\modelTier(matrix)`

| Parameter | Type                                                                  | Optional | Description |
| --------- | --------------------------------------------------------------------- | -------- | ----------- |
| `matrix`  | `func(context.Context, types.ModelTierRequest) (interface {}, error)` |          |             |

