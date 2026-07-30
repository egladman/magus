# magus knowledge graph

magus keeps a deterministic, cache-backed graph of its own domain. Query it to
find and relate entities instead of grepping source.<!-- why --> This skill teaches HOW to use
the tools; the verbs below say WHAT is in this specific workspace. The division is
strict, so this skill never goes stale when a workspace changes - only when the
tool surface does.<!-- /why -->

FAST PATH: in a magus workspace (a magusfile.buzz at the root), any question
shaped like "what exists / what depends on X / where is Y used / how do A and B
relate" is a graph query FIRST - do not open Grep or Glob for it.<!-- why --> Unlike a
grep hit, a graph answer is verified: every edge is extracted from a declared
source or scored by a rubric, and says which.<!-- /why --> If the graph cannot answer,
say so and then fall back<!-- why --> - a silent fallback hides the gap that should be
reported<!-- /why -->.

`MAGUS.md` IS NOT YOUR SOURCE.<!-- why --> It is a generated routing index written for a
HUMAN reading the repo, and it is only as true as its last regeneration - a
workspace whose generate target has not run since the last change describes a
tree that no longer exists. Every fact in it has a live command that cannot be
stale, and those commands scope to a project where the file covers the whole
workspace. Read it as a LAST RESORT: when no daemon is reachable and the CLI is
unavailable too, or when a human explicitly asks what the committed index says.<!-- /why --><!-- terse --> It is a
generated index for humans, true only as of its last regeneration. Last resort
only: no daemon AND no CLI, or a human asking what the committed index says.<!-- /terse -->

## Act in this order

1. Ask the workspace what exists, with the verb that answers your question:
   `magus describe targets` (every target; `-o name` for bare names),
   `magus ls` (every project with its spell, sources, outputs, depends_on),
   `magus describe spells`, `magus describe projects`.<!-- why --> These are live, so they
   are right even mid-change, and they take a `-o json` for machine reading.<!-- /why -->

2. Then reach for the verbs. Prefer the MCP tools. At session start, or after an
   MCP call fails, check `magus status --probe=mcp`. If it is unavailable, tell
   the user once that `magus server start` restores the full agent surface, then
   use the CLI equivalent from the same row below. Do not stop or grep.<!-- why --> CLI
   fallback remains correct, but has no tool discovery or warm daemon graph.<!-- /why -->

   | question                                      | MCP tool        | CLI                                |
   | --------------------------------------------- | --------------- | ---------------------------------- |
   | find and relate entities                      | `magus_query`   | `magus query "<terms>"`            |
   | one node: its edges, provenance, blast radius | `magus_explain` | `magus explain <node>`             |
   | how do two nodes relate                       | `magus_path`    | `magus path <a> <b>`               |
   | where risk concentrates                       | `magus_stats`   | `magus graph stats`                |
   | where a code symbol is defined and used       | `magus_refs`    | `magus refs <symbol>`              |
   | what a branch changed in the graph            | (export + diff) | `magus graph diff <baseline.json>` |

   Prefer these over grep and glob for anything in the magus domain. `magus_refs`
   needs a workspace that declares a SCIP index (`knowledge.symbols` in config);<!-- why --> it
   is the occurrence-shaped def/references answer, so use it over `magus_query` for a
   symbol's fan-in.<!-- /why --> When refs (or `kind:symbol`) reports no match for a symbol that
   plainly exists, the index is probably unbuilt, not the name wrong: `magus status`
   lists each project's symbol-index state; `magus graph build` indexes and rebuilds.

<!-- why -->   The graph relates entities; the evaluated dispatch plan lives one verb over.
<!-- /why -->   `magus describe target <name>` prints, per project, the resolved source globs,
   output globs (the generated files), spells, and policy for that target<!-- why --> - use it
   when the question is "what feeds or comes out of this target", not "what relates
   to it"<!-- /why -->.

## Query grammar

Free-text terms (AND) plus field filters and negation:

- `build` - free text over IDs, labels, and docs
- `kind:spell` - only that node kind
- `project:pkg/foo` - everything the project owns<!-- why -->: the project node, its
  targets, and the files/functions/docs whose source lives under it (nested
  projects claim their own; the root `.` owns only what no nested project does)<!-- /why -->
- `relation:uses` - seed from nodes touching that edge
- `id:build` - substring match on the node ID
- `id:target:*build` - `*` wildcard, matching any run (in a value or a free-text term)
- `-kind:op` - negation, exclude these
- `"exact phrase"` - keep a quoted span as one term

A query returns ranked matches plus their neighborhood, bounded by `--budget`
(default 50).<!-- why --> For a large match set over MCP, pass `limit` and echo the returned
`next_cursor` to fetch the next page.<!-- /why --><!-- terse --> Over MCP, page with `limit` plus the returned
`next_cursor`.<!-- /terse -->

## Reading results

- Reading as a machine? Add `-o json`: every verb returns a stable,
  `schema_version`-stamped OBJECT with a top-level wrapper - key into the
  plural (`.matches`, `.targets`), it is never a bare array. `-o name` prints
  bare IDs for piping.<!-- why --> Do not scrape the human text or trim it with `head`.
  Over MCP the tools already return structured content; nothing to shape.<!-- /why -->
- Node IDs are stable and structured: `<kind>:<qualified-name>`, e.g.
  `target:pkg/foo:build`, `spell:go`, `diagnostic:MGS2001`. Key on them<!-- why -->; a rename
  is a delete plus an add<!-- /why -->.
- Edges are directed and carry a `confidence` - `extracted` (read directly off a
  source) or `inferred` (a rubric score) - plus `provenance` (where it came from).
- Node `attrs` surface metadata<!-- why -->: a project's `engine` and `target_count`, a
  target's inherited `engine`, a doc's `title` and `tags`<!-- /why -->. The `duration_p75_ms`,
  `cache_hit_rate`, `run_samples`, `last_output_ref`, and `last_run_ok` attrs are
  OBSERVED from local run history<!-- why -->, not derived from sources - read them as history, not
  guarantees<!-- /why -->. A target's `last_output_ref` is the `refxxxxxxxx` id of its most recent
  captured run (with `last_run_ok` its `true`/`false` outcome), so `magus query output
<ref>` on it fetches that output<!-- why --> - a target-to-output hop<!-- /why -->. When `knowledge.vcs` is
  enabled, file nodes also carry `vcs_last_commit`, `vcs_last_modified`, and
  `vcs_commits` extracted from git history.
- Every output carries `schema_version`; a bump means the node/edge shape changed.

## Ownership and blast radius

If the repo commits a `CODEOWNERS` file, the graph has `owner` nodes with `owns`
edges to the projects and files they cover.<!-- why --> Combine that with dependency edges to
answer "who owns the blast radius of this change": `magus explain <node>` for the
node's owners and dependents, or `magus query kind:owner` to list owners. Only
declared CODEOWNERS ownership appears - it is not blame-inferred.<!-- /why --><!-- terse --> `magus explain
<node>` for owners plus dependents; `magus query kind:owner` to list. Declared
ownership only, never blame-inferred.<!-- /terse -->

## Across workspaces and neighbors

- `--global` unions every workspace registered in config
  (`knowledge.workspaces`); IDs are namespaced per workspace (`web//spell:go`).
- `magus affected`, `magus insight`, and `magus describe` sit alongside the graph;
  `magus graph export -o json` dumps the whole graph for bulk analysis.
- To show a PR's domain impact, run `magus graph diff --rev main -o markdown` for a CI
  comment<!-- why --> (nodes/edges added, removed, or changed); `--rev` builds the base graph from
  that revision's files, or pass a `graph export -o json` baseline file instead<!-- /why -->.

## Do not render the graph yourself

magus emits; it does not render. To LOOK at the graph, do not draw it: OFFER the
human an export - `magus graph export -o json` (or `-o graphml`) opens directly in
Gephi, yEd, or a browser graph tool.<!-- why --> The emit-never-render rule that governs magus
governs you too.<!-- /why -->

## Fetching current behavior

For flags and behavior this skill does not cover, run any verb with `-h`, and read
the magus documentation site.<!-- why --> Prefer the tools' own output over assumptions.<!-- /why -->
