---
title: Knowledge graph
description: The knowledge graph is a deterministic, cache-backed graph of the magus domain - projects, targets, spells, ops, charms, modules, diagnostics, docs, and buzz sources - that agents and humans query instead of grepping. This page covers the schema, the verbs, the file layout, and how to point external graph tools at an export.
tags:
  [
    knowledge graph,
    query,
    explain,
    path,
    graph,
    schema,
    node-link,
    graphml,
    mcp,
  ]
---

# Knowledge graph

The knowledge graph is a deterministic, cache-backed graph of the magus domain.
Every node and edge is EXTRACTED or rubric-INFERRED from workspace sources - no
LLM pass, ever - so it is safe to rebuild implicitly and byte-for-byte
reproducible from the same inputs. It is assembled from machinery magus already
owns: the verified project dependency DAG, static magusfile extraction, the
spell/module/diagnostic registries, markdown docs, and buzz source parsing.

It exists so agents and humans can ask "what is this, what touches it, how do
these relate" and get a precise answer instead of grepping. Agents reach it over
MCP; humans reach it through three verbs and the `magus graph` home.

## The two-concept model

- **query / explain / path READ the graph** - daily retrieval.
- **`magus graph` IS the graph** - emit it (`deps`), export it (`export`), or
  measure its shape (`stats`).

```sh
magus query "<terms>"       # ranked node matches plus their neighborhood
magus explain <node>        # one node: its edges, provenance, blast radius
magus path <a> <b>          # the shortest chain of edges between two nodes
magus graph stats           # god nodes, orphans, doc coverage
magus graph export -o json  # the whole graph as node-link JSON
magus graph open            # explore it visually in your browser (data stays local)
```

Prefer a picture? `magus graph open` launches the interactive [Graph
Explorer](graph.html) seeded with your own workspace - a force-directed, searchable
view of the same graph. Your data never leaves your machine: it rides in the URL
fragment (or a local loopback server with `--serve`), never reaching the site. This
site's own graph is the [live demo](graph.html).

The committed `MAGUS.md` routing table is the entry point: it lists every node
kind with its count, the query that lists it, and the highest-degree anchor
nodes, so an agent knows what exists before running anything.

## Query grammar

`magus query` takes free-text terms (AND) plus field filters and negation. Terms
are scored with the same leaf-anchored fuzzy match that powers `magus where`.

| Form              | Meaning                                     |
| ----------------- | ------------------------------------------- |
| `build`           | free text: match node IDs, labels, and docs |
| `kind:spell`      | only nodes of that kind                     |
| `project:pkg/foo` | the project node and its targets            |
| `relation:uses`   | seed from nodes touching a `uses` edge      |
| `id:build`        | substring match on the node ID              |
| `-kind:op`        | negation: exclude these                     |
| `"exact phrase"`  | a quoted span stays one term                |

A query resolves terms to seed nodes, then collects the induced neighborhood up
to a node budget (`--budget`, default 50), so a match on a high-degree node
cannot pull in the whole graph.

## Graph Explorer

`magus graph open` opens the graph in an interactive, force-directed
[Graph Explorer](graph.html) in your browser - **privately**. Your graph never
leaves your machine: by default it rides in the link's URL `#fragment` (which
browsers never send to a server), and `--serve` instead hands it to the page from
an ephemeral `127.0.0.1` loopback server that serves once and stops. The hosted
page is static; it decodes or fetches the graph locally.

```sh
magus graph open           # default: gzip'd into the URL fragment (small/medium graphs)
magus graph open --serve   # loopback server (no size limit; serves once, then stops)
magus graph open --print   # print the URL instead of opening a browser
magus graph open --url <base>   # point at a self-hosted mirror of the explorer
```

The explorer's filter box speaks the **same fielded grammar** as `magus query`
(`kind:`, `project:`, `relation:`, `id:`, free text, `"quotes"`, `-negation`); a
query dims non-matching nodes so the subgraph stands out. Beyond the filter:
double-click a node for its **local graph** (its neighborhood, `[`/`]` to change
depth), click a legend color to isolate a kind, and use the **hubs**/**orphans**
lenses (the visual twin of `magus graph stats`). The page is fully client-side and
data-agnostic - it also loads any `graph.json` from `magus graph export -o json`
via the Open-file button or drag-and-drop. This site's own graph is the
[live demo](graph.html).

## Schema

A node is a magus-domain entity with a stable, human-readable ID
(`<kind>:<qualified-name>`, e.g. `target:pkg/foo:build`). The ID is stable across
builds so external consumers and agent memory can key on it. A rename is a
delete-plus-add.

Node kinds: `project`, `target`, `spell`, `op`, `charm`, `module`, `method`,
`diagnostic`, `doc`, `file`, `function`, `import`, `rationale`.

Nodes also carry static metadata the extractors already parse, surfaced as
attributes so `magus explain` answers a question without a second describe: a
project reports its `engine` and `target_count`, each target inherits its
project's `engine`, and a doc page carries its frontmatter `title` and `tags`.
Attributes are additive and absent when unknown, so they never bump the schema
version.

Edges are directed and carry provenance and a confidence tag - `extracted` (1.0,
from a parseable source) or `inferred` (a rubric score, from a fuzzy match).

Relations: `depends_on`, `contains`, `uses`, `calls`, `imports`, `references`,
`documents`, `rationale_for`.

Both node-link JSON and GraphML carry a `schema_version`; external consumers and
agent skills should check it, since a bump is a changelog event.

## File layout

The graph lives under the cache dir at `.magus/knowledge/`, cache-owned and NOT
committed by default - the build is cheap and deterministic, so committing
derived data buys nothing (`export` exists for teams that want a snapshot).

```text
.magus/knowledge/
  manifest.json        per-shard fingerprints and counts (the routing index)
  shards/<name>.json   one file per shard; SHARDS ARE AUTHORITATIVE
```

There is no continuously maintained merged `graph.json`: at scale, rewriting a
merged file on every edit is an O(graph) write. Merging happens in memory at load
time; the merged export is produced on demand. Shards are per-project plus
singletons for the registry (spells/modules/diagnostics), docs, buzz sources, and
run history (`@runtime`, below). A query loads the store, fingerprint-checks each
shard, and rebuilds only the stale ones - the "cache that gets hit first". First
run pays a full build; steady state is a fingerprint check. `--refresh` forces a
full rebuild.

Two optional knobs bound and share the store. `knowledge.max_size_mb` soft-caps
the shard directory: over the cap, least-recently-used shard files are evicted
(their manifest entries stay, so an evicted shard is restored from the remote
cache or rebuilt on the next query; 0, the default, is unlimited). When a remote
build cache is configured, deterministic shards ride it - pushed on build,
restored by fingerprint - so teammates and CI can reuse them. The `@runtime` shard
is never pushed: it is local run history, not shareable derived data.

## Runtime enrichment

Beyond the static graph, magus records which diagnostics (`MGSxxxx` codes) each
target trips during real runs, as `emits` edges in the isolated `@runtime` shard.
A run captures every fired diagnostic through one sink that also feeds the report
stream, and persists the set to `<cache>/knowledge/runtime.json`. This answers
"what has this target tripped" - history the static `documents` edge cannot. The
same shard also folds observed performance onto target nodes from the local timing
history: `duration_p75_ms`, `cache_hit_rate`, and `run_samples`, so an agent
planning work sees a target's cost without a separate history query. Timings for a
target no longer in any magusfile are dropped rather than left as phantom nodes.
This is the graph's only non-deterministic input, so it is quarantined: a distinct
shard, excluded from remote export, derived from local run records rather than
workspace sources.

## Exporting to external tools

magus emits; it does not render. To look at the graph, export it and open the
file in a graph tool - files are the interface.

```sh
magus graph export -o json > graph.json       # node-link JSON (NetworkX, D3, ...)
magus graph export -o graphml > graph.graphml  # GraphML (Gephi, yEd, ...)
```

For a specific neighborhood rather than the whole graph, `--select` reuses the
query engine, and the layout formats become available (they are unreadable on the
full graph, so they require a scope):

```sh
magus graph export --select "kind:spell go" -o mermaid
magus graph export --select "project:pkg/foo" --budget 80 -o dot
```

## Global graph (across workspaces)

An org running magus across many repos can query all of them at once. Register
extra workspace roots in config, then pass `--global`:

```yaml
# magus.yaml
knowledge:
  workspaces:
    - ../api
    - ../web
```

```sh
magus query "kind:spell" --global   # matches across every registered workspace
magus graph stats --global          # union shape across repos
```

`--global` is available on query, explain, path, and `magus graph export`/`stats`.
Each workspace's node IDs are namespaced by the workspace (`api//spell:go`,
`web//spell:go`) so IDs from different repos cannot collide; the unqualified ID
stays a readable substring, so `magus explain go --global` still resolves. A
registered workspace that cannot be opened is skipped rather than failing the
query. There is no cross-workspace edge inference - a union with qualified IDs
only.

## Extraction diagnostics

When extraction cannot resolve something cleanly it records a silent
[`MGS7xxx`](codes/knowledge/README.md) code as a node attribute (visible via
`magus explain`), rather than logging - so an implicit rebuild stays quiet while
the ambiguity stays queryable. The first two are
[MGS7001](codes/knowledge/MGS7001.md) (a buzz import that resolves to no file)
and [MGS7002](codes/knowledge/MGS7002.md) (a doc citing an unregistered code).

## For agents

The MCP daemon exposes the verbs as tools: `magus_query`, `magus_explain`,
`magus_path`, and `magus_stats`. See [MCP](mcp.md) for wiring. Prefer these over
grep to find and relate magus-domain entities; start from the `MAGUS.md` routing
table, which is already in context in a fresh clone.

For a large match set, `magus_query` pages: pass `limit` to cap the matches per
response and echo the returned `next_cursor` to fetch the next page. The cursor is
stateless and self-validating - it carries the query and a graph fingerprint, so a
cursor reused against a different query or a graph that changed between pages is
rejected rather than returning an incoherent slice.
