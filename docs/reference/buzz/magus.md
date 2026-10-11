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

`import "magus"` is how you reach any of this. The namespace is an ordinary host module, like `fs` or `vcs`: without the import line `magus` is undefined, and the import is what attaches these signatures to your call sites. It resolves in a `magus buzz` script as well as in a magusfile, and a script run inside a workspace reads that workspace: `describe.project`, `affected`, `projectGraph`, `where`, `insight`, the knowledge-graph reads (`query`, `explain`, `path`, `refs`, `stats`, `importGraph`, `dir`, `dirs`, `layer`, `neighborhood`) and `output` all answer in-process, and so does `magus\job` (list, put, register, exit, wait, clear): the job store an orchestrating agent declares about work it handed out (see types.Job). The `magus job` CLI subcommand is a third write door onto the same rows: ls and describe read, fork declares a row, exec records a worker's landed base, and wait blocks on a dependency. Only the members that DECLARE into the workspace being loaded (`magus\project`, the provider selections above) raise [MGS1022](../codes/magusfile/MGS1022.md) in a script: there is nothing for them to declare into. Run a script outside any workspace and the reading members raise it too, since there is no workspace to read. The nested-command methods (`cmd`, `run`, `doctor`, and the `magus\describe` methods that fork) work there either way and discover the workspace themselves.

> **Naming convention:** import the module under its bare name (`import "magus"`), reach members with a backslash, and call methods in `camelCase`: `magus\someMethod`.

## Methods

### cmd

Escape hatch: run `magus <sub> <args>` for a subcommand with no dedicated method (status, affected, agent, graph, ...). Its signature is the typed methods' signature with the subcommand pushed in front: magus\cmd(sub, args, [opts]) beside magus\run(args, [opts]), same argv, same opts, same ExecResult. The SUBCOMMAND is a typed argument rather than args[0] because it is the part of the invocation magus can reason about - it stays readable in the signature and greppable in the source, while the remaining argv stays free-form. Prefer the dedicated methods (run, doctor, the magus\describe methods) when one exists - magus\cmd warns when sub, or a describe noun, names one that has; a describe noun with no method (job), and any noun's text in an explicit -o format, is reached here without a warning. Returns {stdout, stderr, code, ok}; raises on non-zero exit unless opts.allow_failure is true. opts.root sets the global --root workspace; opts.dir runs it in another directory (relative to the target's, like proc\exec); opts.quiet captures the output without echoing it to the console; opts.stdin feeds the child's standard input, which is how a credential reaches a subcommand (`graph push`) without passing through a process listing or a run log.

**Signature:** `magus\cmd(sub, args, [opts]) -> ExecResult` - [source](https://github.com/egladman/magus/blob/main/std/magus.go#L1497)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `sub`     | `string`         |          |             |
| `args`    | `[]string`       |          |             |
| `opts`    | `map[string]any` | yes      |             |

**Returns:** map[string]any

### affected

Compute the VCS-affected project set against base (empty uses the configured base ref): {base, changed, seed, filesBySeed, affected}. Served in-process from the workspace on the context - no subprocess. Raises when the diff cannot be computed, rather than reporting an empty set, since an empty set and an uncomputable one mean opposite things to a caller deciding what to build.

**Signature:** `magus\affected([base]) -> Affected` - [source](https://github.com/egladman/magus/blob/main/std/magus.go#L1359)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `base`    | `string` | yes      |             |

**Returns:** map[string]any

### projectGraph

The project dependency DAG as {nodes, dependsOn, blastRadius}. nodes is in TOPOLOGICAL order, so iterating it is already a valid build order; dependsOn gives each node's direct predecessors and blastRadius how many projects it can transitively affect. Served in-process from the workspace on the context - no subprocess.

**Signature:** `magus\projectGraph() -> Graph` - [source](https://github.com/egladman/magus/blob/main/std/magus.go#L1479)

**Returns:** map[string]any

### where

Return the project path containing dir, or null when dir is inside no project. Served in-process from the workspace on the context - no subprocess.

**Signature:** `magus\where(dir) -> string` - [source](https://github.com/egladman/magus/blob/main/std/magus.go#L1374)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `dir`     | `string` |          |             |

**Returns:** string

### raise

Fail with a CODED diagnostic instead of a bare string, so a caller can branch on the code: `catch (e) { if (e.code == "ACME1001") ... }`. code is yours to define and namespace - anything but the MGS prefix, which is magus's own. opts.cause is the error being wrapped, usually the value from an inner catch; it is appended to the message the way Go's %w renders one, and the failure it came from stays reachable underneath. opts.url is the page documenting the code, rendered as the `see:` line the CLI prints under its own diagnostics.

**Signature:** `magus\raise(code, message, [opts])` - [source](https://github.com/egladman/magus/blob/main/std/magus.go#L1418)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `code`    | `string`         |          |             |
| `message` | `string`         |          |             |
| `opts`    | `map[string]any` | yes      |             |

### run

Run `magus run <args>` recursively in the target's project directory and capture its output. Child invocations share the parent's concurrency budget over the local socket. Returns {stdout, stderr, code, ok}; raises on non-zero exit unless opts.allow_failure is true. opts.root sets the global --root workspace; opts.dir runs it in another directory (relative to the target's, like proc\exec); opts.quiet captures the output without echoing it to the console; opts.stdin feeds the child's standard input.

**Signature:** `magus\run(args, [opts]) -> ExecResult` - [source](https://github.com/egladman/magus/blob/main/std/magus.go#L1531)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `args`    | `[]string`       |          |             |
| `opts`    | `map[string]any` | yes      |             |

**Returns:** map[string]any

### insight

Every VCS-history lens as one typed report: {hotspots, affinity, ownership, trend, volatility, unreferenced}. Annotate the result `> InsightReport` for compile-checked field access - `r.ownership.projects` gives each project's primary author and bus-factor flag, `r.hotspots.files` the churn-by-complexity ranking, `r.volatility` the targets that flapped. Takes the window as `{commits, since}` and renders nothing - presentation is the caller's job. Read straight off the workspace already open on the context - no subprocess, no second workspace load, no JSON round-trip. Works from a magusfile target and from a `magus buzz` script run inside a workspace; raises MGS1022 only when there is no workspace to read.

**Signature:** `magus\insight([opts]) -> InsightReport` - [source](https://github.com/egladman/magus/blob/main/std/magus.go#L1575)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `opts`    | `map[string]any` | yes      |             |

**Returns:** map[string]any

### query

Search the knowledge graph: {definition, schemaVersion, query, budget, matchCount, offset, matches, nodes, links, answer}. Annotate the result `> QueryResult`. query is free text plus field matchers (kind=spell, project=pkg/foo, relation=uses, kind!=op, id=~build$). A query that seeds code symbols (kind=symbol) reads the symbol shards too; every other query reads the domain graph. opts.budget caps the neighborhood (default 50); opts.limit and opts.offset window the matches while matchCount stays the total. Read answer.verdict before trusting zero matches: `unknown` means part of the workspace had no symbol index. An unknown option raises. Read in-process from the workspace on the context; raises MGS1022 outside one.

**Signature:** `magus\query(query, [opts]) -> QueryResult` - [source](https://github.com/egladman/magus/blob/main/std/magus_graph.go#L105)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `query`   | `string`         |          |             |
| `opts`    | `map[string]any` | yes      |             |

**Returns:** map[string]any

### explain

One knowledge-graph node's context card: {definition, schemaVersion, node, blastRadius, out, in, docsURL, resolution}. Annotate the result `> ExplainResult`. node is a node ID (target:pkg/foo:build), a workspace path (internal/httpx), or a name that resolves to one. A path resolves to its dir or file node exactly before any ranked match; resolution says which happened (id, path or fuzzy), and a caller anchoring by path refuses fuzzy. A path between two nodes is magus\path. Raises when node resolves to nothing, naming magus\refs when the name could be a code symbol this graph does not load. Read in-process from the workspace on the context; raises MGS1022 outside one.

**Signature:** `magus\explain(node) -> ExplainResult` - [source](https://github.com/egladman/magus/blob/main/std/magus_graph.go#L136)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `node`    | `string` |          |             |

**Returns:** map[string]any

### path

The shortest chain of edges between two knowledge-graph nodes: {definition, schemaVersion, from, to, found, steps}. Annotate the result `> PathResult`. Edges are walked in both directions. opts.relations limits the hops to those relations, so found false under it means no path of those relations. A resolved pair with no connection returns found false; an endpoint that resolves to nothing, an unknown option, and an unknown relation raise. The endpoints are `node` and `to` rather than from and to because `from` is a reserved Buzz word. Read in-process from the workspace on the context; raises MGS1022 outside one.

**Signature:** `magus\path(node, to, [opts]) -> PathResult` - [source](https://github.com/egladman/magus/blob/main/std/magus_graph.go#L173)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `node`    | `string`         |          |             |
| `to`      | `string`         |          |             |
| `opts`    | `map[string]any` | yes      |             |

**Returns:** map[string]any

### refs

Where a code symbol is defined and every file that references it: {definition, schemaVersion, symbol, label, fileCount, refCount, defs, refs, answer}. Annotate the result `> RefsResult`. symbol is a symbol node ID or a name that resolves to one, drawn from the workspace's declared SCIP indexes. A symbol nothing defines is an answer, not a raise: answer.verdict says whether that is a verified absence or a blind spot. opts.limit and opts.offset window refs while fileCount and refCount stay the totals. Read in-process from the workspace on the context; raises MGS1022 outside one.

**Signature:** `magus\refs(symbol, [opts]) -> RefsResult` - [source](https://github.com/egladman/magus/blob/main/std/magus_graph.go#L204)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `symbol`  | `string`         |          |             |
| `opts`    | `map[string]any` | yes      |             |

**Returns:** map[string]any

### stats

The knowledge graph's shape: {definition, nodeCount, edgeCount, gods, orphans, coverage, isolatedCount, componentCount, largestComponentSize}. Annotate the result `> KnowledgeStats`. gods are the most connected nodes, where structural risk concentrates; orphans are docs that document nothing and spells no target uses. kind scopes every section to one node kind (spell, target, doc, ...); omit it for the whole graph. Read in-process from the workspace on the context; raises MGS1022 outside one.

**Signature:** `magus\stats([kind]) -> KnowledgeStats` - [source](https://github.com/egladman/magus/blob/main/std/magus_graph.go#L244)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `kind`    | `string` | yes      |             |

**Returns:** map[string]any

### importGraph

The workspace's package import graph: {indexed, packages}, packages mapping each package directory ("." for the root) to the sorted package directories it imports, read off the workspace's declared SCIP indexes. Annotate the result `> ImportGraph`. Test files, packages outside the workspace, and self-imports are left out. indexed is false when no symbol index was ingested, and packages is then empty because nobody looked, not because nothing imports anything: a caller checking drift refuses on it rather than reporting none. Read in-process from the workspace on the context; raises MGS1022 outside one.

**Signature:** `magus\importGraph() -> ImportGraph` - [source](https://github.com/egladman/magus/blob/main/std/magus_graph.go#L258)

**Returns:** map[string]any

### symbolIndexDigest

A digest of the symbol index the graph members load: {digest, indexed, projects, gaps}. Annotate the result `> SymbolIndexDigest`. digest is a hex SHA-256 over each loaded project's symbol shard fingerprint in project path order, so it moves exactly when magus\importGraph could answer differently; a target writes it as a declared output that a reader of the index keys its cache on, instead of skip_cache. The knowledge graph is built first, so the digest names the index files on disk now. indexed is false and digest empty when no index was ingested. gaps are the projects declaring an index magus could not read: a digest with gaps is stable but partial. Read in-process from the workspace on the context; raises MGS1022 outside one, and raises when the gap probe cannot run.

**Signature:** `magus\symbolIndexDigest() -> SymbolIndexDigest` - [source](https://github.com/egladman/magus/blob/main/std/magus_index.go#L25)

**Returns:** map[string]any

### precedents

The precedents the workspace's merged symbol indexes establish, the rows `Graph.Precedents` mines: {precedents, indexes}, keyed as JSON is, with no Buzz object mirroring it. Each precedent is {family, scope, key, follow, cohort, share, established, cited, departures}: follow of cohort cases share one shape, established when the cohort and share clear the conformance gate, and departures are the cases that do not. indexes is each declared symbol index as `magus status` judges it ({project, op, language, freshness, detail}), judged just before the graph is read: an index not `up-to-date` gave the rows nothing or something old, so a gate on the rows checks indexes first and fails rather than reading silence as agreement. Declared outputs are never counted. Read in-process from the workspace on the context; raises MGS1022 outside one.

**Signature:** `magus\precedents() -> map[string]any` - [source](https://github.com/egladman/magus/blob/main/std/magus_graph.go#L354)

**Returns:** map[string]any

### symbols

Every non-test, non-generated symbol's declaration and doc comment in the workspace's merged symbol indexes, whatever language indexed them, as data: {symbols, indexes}, keyed as JSON is, with no Buzz object mirroring it. Each symbol is {node, source, language, name, kind, owner, doc}: source is the declaration's path and line, since an index records no position inside a doc; kind is function, method, type, interface, struct or value, or empty when the naming index read no shape; owner is the enclosing type of a member; doc is the whole comment with its lines kept. Nothing here judges the text: a magusfile rule reads it and decides. indexes is each declared symbol index as `magus status` judges it, judged just before the graph is read, as magus\precedents reports it: an index not `up-to-date` gave the symbols nothing or something old, so a gate on them checks indexes first. Test files and declared outputs are never listed. Read in-process from the workspace on the context; raises MGS1022 outside one.

**Signature:** `magus\symbols() -> map[string]any` - [source](https://github.com/egladman/magus/blob/main/std/magus_graph.go#L368)

**Returns:** map[string]any

### dir

One workspace directory as the knowledge graph holds it: {path, id, layer, language, imports, importedBy, importsIndexed, calls, calledBy, children, files}. Annotate the result `> Dir`. path is workspace-relative (internal/httpx). imports and importedBy are the package directories it imports and that import it; importsIndexed false means no symbol index read this directory, so empty lists there say nothing. calls and calledBy are DirCall records, one per `magus:calls` marker, with the transport it declares. layer is what magus\project's "layers" declares for it. Raises MGS7005 when the graph holds no dir node for path, naming the nearest one when a typo is likely, so a figure never draws a box for a directory that is not there. Read in-process from the workspace on the context; raises MGS1022 outside one.

**Signature:** `magus\dir(path) -> Dir` - [source](https://github.com/egladman/magus/blob/main/std/magus_graph.go#L384)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `path`    | `string` |          |             |

**Returns:** map[string]any

### dirs

Every directory whose workspace path matches glob, as Dir records sorted by path. Annotate the result `> [Dir]`. glob is a doublestar pattern (internal/**). opts is a DirsOptions: layer keeps one declared layer, and a layer nothing declares raises MGS7006 rather than matching nothing; language keeps one package language; depth bounds how many segments below the glob's literal prefix a match may sit (0 is unbounded). An unknown option raises. Read in-process from the workspace on the context; raises MGS1022 outside one.

**Signature:** `magus\dirs(glob, [opts]) -> [Dir]` - [source](https://github.com/egladman/magus/blob/main/std/magus_graph.go#L406)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `glob`    | `string`         |          |             |
| `opts`    | `map[string]any` | yes      |             |

**Returns:** any

### layer

One layer magus\project's "layers" key declares: {name, declared, dirs}. Annotate the result `> Layer`. declared are the directories and globs declared for it; dirs are the Dir records it covers. Raises MGS7006 on a name no declaration uses, listing the declared ones. Read in-process from the workspace on the context; raises MGS1022 outside one.

**Signature:** `magus\layer(name) -> Layer` - [source](https://github.com/egladman/magus/blob/main/std/magus_graph.go#L442)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `name`    | `string` |          |             |

**Returns:** map[string]any

### neighborhood

The knowledge subgraph around one focus node: {definition, schemaVersion, focus, resolution, options, nodes, links, folds, answer}. Annotate the result `> NeighborhoodResult`. focus is a node ID, a workspace path, or a name; resolution says how it was reached. opts is a NeighborhoodOptions: depth is the most hops (0 means 1); relations are the only relations walked; direction is out, in, or empty for both; collapse folds every source node under each workspace path prefix into that prefix's dir node, longest prefix winning, and folds lists what each absorbed. Read answer.verdict before trusting a thin result: an imports walk with no symbol index is unknown, not absent. An unknown option, relation or direction raises, as does a focus that resolves to nothing. Read in-process from the workspace on the context; raises MGS1022 outside one.

**Signature:** `magus\neighborhood(focus, [opts]) -> NeighborhoodResult` - [source](https://github.com/egladman/magus/blob/main/std/magus_graph.go#L464)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `focus`   | `string`         |          |             |
| `opts`    | `map[string]any` | yes      |             |

**Returns:** map[string]any

### output

One target run's captured output by its ref: {ref, project, target, failed, durationMs, output}. Annotate the result `> OutputRecord`. ref is an output ref (out1a2b3c) or a unique prefix of one. Raises on a value that is not a ref, a prefix that matches several, and a ref this checkout's output store does not hold: output lives in the checkout that ran the target. Read in-process from the workspace on the context, or inside a guard rule from the checkout the guard judges; raises MGS1022 outside both.

**Signature:** `magus\output(ref) -> OutputRecord` - [source](https://github.com/egladman/magus/blob/main/std/magus_graph.go#L629)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `ref`     | `string` |          |             |

**Returns:** map[string]any

### impact

The blast radius of a changeset: {base, changedFileCount, changedFiles, seedProjects, affectedProjects, notes}. Each affected project carries whether it was a seed and, for a seed, the changed files in it. Annotate the result `> Impact`. This is `magus affected --impact`: the report, with no target run. opts.commits caps the commits scanned; opts.since bounds the window (90d, 12w, 6mo, 1y).

**Signature:** `magus\impact([base], [opts]) -> Impact` - [source](https://github.com/egladman/magus/blob/main/std/magus.go#L1558)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `base`    | `string`         | yes      |             |
| `opts`    | `map[string]any` | yes      |             |

**Returns:** map[string]any

### diff

Read the working tree's uncommitted changes, annotated and ordered by what they can break: for each file the owning project, whether it is a declared `output` (generated - the source edit is the review), how widely its changed symbols are referenced (`reach`), whether another project can see them (`visibility`), observed `coverage`, how often it has been changing (`churn`), and which agent sessions wrote it (`touches`). Files come back in the order magus recommends READING them - generated last whatever its reach, then widest reach first - so a caller renders the list as given rather than sorting it again. Returns a typed Diff envelope; branch on `role` and `visibility` rather than grepping text. opts.rev reviews a committed range written base...head instead of the working tree, which is what a caller running where the tree is clean (a CI checkout) has to pass to see anything at all. opts.patch reviews a unified diff given as text instead, the way `magus diff --patch -` reads one: a pull request's patch, for files that may not match the tree. opts.baseline is a `magus graph export --symbols -o json` of the base: with it every changed symbol carries what the change did to it (`change` is added, removed, signature, or body) and `api` carries the semver bump that proves, a floor and never a ceiling. Each symbol the change adds, renames or re-signs carries `checks`: what the conformance checks found against how the rest of the workspace declares the same kind of thing, with opts.minCohort and opts.minShare as their silence gates (default 5 and 0.8). opts.from reads a review an earlier `magus diff -o json` saved instead of computing it again, so several readers of one change pay for one diff. Runs a nested magus, so it needs no workspace on the context and works from a `magus buzz` script.

**Signature:** `magus\diff([opts]) -> Diff` - [source](https://github.com/egladman/magus/blob/main/std/magus.go#L1938)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `opts`    | `map[string]any` | yes      |             |

**Returns:** map[string]any

### doctor

Validate the workspace and return what every check found: {workspace, checks, summary}, each check {name, status, message, details} with status `ok`, `fail`, or `advice` (advice is worth knowing and never a gate). Annotate the result `> DoctorReport` for compile-checked field access. A caller branches on a check's status rather than grepping console text for the word fail. It does NOT raise when a check fails: doctor exits non-zero precisely when it has something to report, and raising would discard the report. Gate on `summary.fail` instead, which says more than an exit code does. It DOES raise when the underlying `magus doctor` subprocess itself cannot be launched or its output cannot be decoded - an infrastructure failure, not a check result. opts.root sets the global --root workspace; opts.dir runs it in another directory (relative to the target's, like proc\exec).

**Signature:** `magus\doctor(args, [opts]) -> DoctorReport` - [source](https://github.com/egladman/magus/blob/main/std/magus.go#L1547)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `args`    | `[]string`       |          |             |
| `opts`    | `map[string]any` | yes      |             |

**Returns:** map[string]any

### clean

Remove the declared outputs of the selected projects: {removed, tracked, dryRun}. Arguments are `magus clean`'s: project paths, `--cache`, `--dry-run`. With no projects, the cwd project is selected, or the whole workspace from the root. Tracked outputs stay, because they are committed. Annotate the result `> CleanReport`. opts.root and opts.dir as on doctor.

**Signature:** `magus\clean(args, [opts]) -> CleanReport` - [source](https://github.com/egladman/magus/blob/main/std/magus.go#L1553)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `args`    | `[]string`       |          |             |
| `opts`    | `map[string]any` | yes      |             |

**Returns:** map[string]any

### attention

List the OPEN attention requests of this repository's session store: {requests, store}, each request {id, outcome, source, where, lease, message, ...} as `magus session attention -o json` reports them. Read-only by design: a magusfile may refuse to proceed while a request is open, but disposing one is a human act (see the workspace doctrine's Manual-on-purpose table), so no method here closes anything - the person runs `magus session dispose <id> -reason <text>`. Runs a nested magus, so it works from a `magus buzz` script as well as a magusfile; opts.root and opts.dir as on doctor. Raises only when the subprocess cannot run or its output cannot decode.

**Signature:** `magus\attention(args, [opts]) -> map[string]any` - [source](https://github.com/egladman/magus/blob/main/std/magus.go#L1538)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `args`    | `[]string`       |          |             |
| `opts`    | `map[string]any` | yes      |             |

**Returns:** map[string]any

### diagnoseDrift

Diagnose why a generate gate's declared outputs drifted and RETURN the verdict {drifted, code, message, url, files} so the caller decides whether to fail or warn. Pass the target's output globs and (optional) input globs, project-relative. code is MGS4006 when a declared input changed (real drift, commit it), MGS4005 when the inputs are unchanged but a dev build produced differing output (version/tool skew, not your change), or MGS4003 when a release build's identical inputs still differ (a reproducibility bug). files are the drifted outputs as Paths based at the repository root. drifted is false with every field zero when the outputs are clean. It lives here rather than on vcs because choosing between those codes is magus policy; vcs only supplies the probe. Composes vcs\status; does not replace it.

**Signature:** `magus\diagnoseDrift(outputs, [inputs]) -> DriftResult` - [source](https://github.com/egladman/magus/blob/main/std/magus.go#L2179)

| Parameter | Type       | Optional | Description |
| --------- | ---------- | -------- | ----------- |
| `outputs` | `[]string` |          |             |
| `inputs`  | `[]string` | yes      |             |

**Returns:** any

### bustCache

Invalidate the build cache. Escape hatch - prefer modeling missing inputs as Sources. No arg clears all; a project path clears one project.

**Signature:** `magus\bustCache([project_path])` - [source](https://github.com/egladman/magus/blob/main/std/magus.go#L1170)

| Parameter      | Type     | Optional | Description |
| -------------- | -------- | -------- | ----------- |
| `project_path` | `string` | yes      |             |

### hasCharm

True when execution charm `name` is active, letting a target body branch on a charm carried in context (e.g. has_charm("rw")).

**Signature:** `magus\hasCharm(name) -> bool` - [source](https://github.com/egladman/magus/blob/main/std/magus.go#L1163)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `name`    | `string` |          |             |

**Returns:** bool

### project

Declare this directory's project: its spell, sources, outputs, and options. A magusfile calls it once at top level. Raises MGS1022 in a `magus buzz` script, which has no workspace to declare into.

**Signature:** `magus\project(config, [opts])`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `config`  | `any` |          |             |
| `opts`    | `any` | yes      |             |

### skills

Every skill this workspace offers an agent, sorted by name then form: each {name, description, source, form, body, current}. source is `shipped` for magus's own catalog, which yields a `short` and a `full` entry per skill, or `local` for a hand-authored skill found in an installed skills directory (a wired harness's skill paths, where `magus agent install` writes), which yields one entry with form `full`. body is exactly what an agent loads: the SKILL.md text with its frontmatter excluded, the generated footer kept. current is true when every installed copy of that entry is byte-equal to what this magus would install, false when one differs or none is installed, and always true for a local skill. opts.name selects one skill and raises on a name nothing offers, naming the near matches; opts.form (`short`, `full`, or `both`, the default) narrows the shipped entries and leaves local ones alone. An unknown option raises. Pair it with magus\job.put and magus\cmd("describe", ["job", id]) to hand a worker its brief and its skills from magus itself rather than from pasted files. Read from the workspace on the context; raises MGS1022 in a script run outside one.

**Signature:** `magus\skills([opts]) -> [Skill]`

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `opts`    | `map[string]any` | yes      |             |

**Returns:** any

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

