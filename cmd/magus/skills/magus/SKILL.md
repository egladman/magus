---
name: magus-knowledge-graph
description: Use the magus knowledge graph to find and relate magus-domain entities (projects, targets, spells, ops, charms, modules, diagnostics, docs) instead of grepping. Trigger when working in a repo that uses magus and you need to know what exists, what depends on what, or how two entities relate.
---

# magus knowledge graph

magus keeps a deterministic, cache-backed graph of its own domain. Query it to
find and relate entities instead of grepping source. This skill teaches HOW to use
the tools; the repo's committed `MAGUS.md` says WHAT is in this specific workspace.
The division is strict, so this skill never goes stale when a workspace changes -
only when the tool surface does.

## Act in this order

1. Read `MAGUS.md` first. Its routing table is committed and usually already in
   context: it lists every node kind with a count, the exact query to list it, and
   the highest-degree anchor nodes. Consult it before running anything, so your
   first query is precise rather than a guess.

2. Then reach for the verbs. Prefer the MCP tools; the CLI is the fallback when no
   magus daemon is running.

   | question | MCP tool | CLI |
   | --- | --- | --- |
   | find and relate entities | `magus_query` | `magus query "<terms>"` |
   | one node: its edges, provenance, blast radius | `magus_explain` | `magus explain <node>` |
   | how do two nodes relate | `magus_path` | `magus path <a> <b>` |
   | where risk concentrates | `magus_stats` | `magus graph stats` |
   | where a code symbol is defined and used | `magus_refs` | `magus refs <symbol>` |
   | what a branch changed in the graph | (export + diff) | `magus graph diff <baseline.json>` |

   Prefer these over grep and glob for anything in the magus domain. `magus_refs`
   needs a workspace that declares a SCIP index (`knowledge.symbols` in config); it
   is the occurrence-shaped def/references answer, so use it over `magus_query` for a
   symbol's fan-in.

## Query grammar

Free-text terms (AND) plus field filters and negation:

- `build` - free text over IDs, labels, and docs
- `kind:spell` - only that node kind
- `project:pkg/foo` - a project and its targets
- `relation:uses` - seed from nodes touching that edge
- `id:build` - substring match on the node ID
- `id:target:*build` - `*` wildcard, matching any run (in a value or a free-text term)
- `-kind:op` - negation, exclude these
- `"exact phrase"` - keep a quoted span as one term

A query returns ranked matches plus their neighborhood, bounded by `--budget`
(default 50). For a large match set over MCP, pass `limit` and echo the returned
`next_cursor` to fetch the next page.

## Reading results

- Node IDs are stable and structured: `<kind>:<qualified-name>`, e.g.
  `target:pkg/foo:build`, `spell:go`, `diagnostic:MGS2001`. Key on them; a rename
  is a delete plus an add.
- Edges are directed and carry a `confidence` - `extracted` (read directly off a
  source) or `inferred` (a rubric score) - plus `provenance` (where it came from).
- Node `attrs` surface metadata: a project's `engine` and `target_count`, a
  target's inherited `engine`, a doc's `title` and `tags`. The `duration_p75_ms`,
  `cache_hit_rate`, and `run_samples` attrs are OBSERVED from local run history, not
  derived from sources - read them as history, not guarantees. When `knowledge.vcs` is
  enabled, file nodes also carry `vcs_last_commit`, `vcs_last_modified`, and
  `vcs_commits` extracted from git history.
- Every output carries `schema_version`; a bump means the node/edge shape changed.

## Ownership and blast radius

If the repo commits a `CODEOWNERS` file, the graph has `owner` nodes with `owns`
edges to the projects and files they cover. Combine that with dependency edges to
answer "who owns the blast radius of this change": `magus explain <node>` for the
node's owners and dependents, or `magus query kind:owner` to list owners. Only
declared CODEOWNERS ownership appears - it is not blame-inferred.

## Across workspaces and neighbors

- `--global` unions every workspace registered in config
  (`knowledge.workspaces`); IDs are namespaced per workspace (`web//spell:go`).
- `magus affected`, `magus insight`, and `magus describe` sit alongside the graph;
  `magus graph export -o json` dumps the whole graph for bulk analysis.
- To show a PR's domain impact, run `magus graph diff --rev main -o markdown` for a CI
  comment (nodes/edges added, removed, or changed); `--rev` builds the base graph from
  that revision's files, or pass a `graph export -o json` baseline file instead.

## Do not render the graph yourself

magus emits; it does not render. To LOOK at the graph, do not draw it: OFFER the
human an export - `magus graph export -o json` (or `-o graphml`) opens directly in
Gephi, yEd, or a browser graph tool. The emit-never-render rule that governs magus
governs you too.

## Fetching current behavior

For flags and behavior this skill does not cover, run any verb with `-h`, and read
the magus documentation site. Prefer the tools' own output over assumptions.
