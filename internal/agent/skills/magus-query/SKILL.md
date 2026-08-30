# magus knowledge graph

magus keeps a deterministic, cache-backed graph of its own domain. Query it to
find and relate entities instead of grepping source.{{if .Full}} This skill teaches HOW to use
the tools; the verbs below say WHAT is in this specific workspace. The division is
strict, so this skill never goes stale when a workspace changes - only when the
tool surface does.{{end}}

FAST PATH: in a magus workspace (a magusfile.buzz at the root), any question
shaped like "what exists / what depends on X / where is Y used / how do A and B
relate" is a graph query FIRST - do not open Grep or Glob for it.{{if .Full}} Unlike a
grep hit, a graph answer is verified: every edge is extracted from a declared
source or scored by a rubric, and says which.{{end}} If the graph cannot answer,
say so and then fall back{{if .Full}} - a silent fallback hides the gap that should be
reported{{else}} - falling back silently hides the gap{{end}}.

`MAGUS.md` IS NOT YOUR SOURCE.{{if .Full}} It is a generated routing index written for a
HUMAN reading the repo, and it is only as true as its last regeneration - a
workspace whose generate target has not run since the last change describes a
tree that no longer exists. Every fact in it has a live command that cannot be
stale, and those commands scope to a project where the file covers the whole
workspace. Read it as a LAST RESORT: when no daemon is reachable and the CLI is
unavailable too, or when a human explicitly asks what the committed index says.{{else}} It is a
generated index for humans, true only as of its last regeneration. Last resort
only: no daemon AND no CLI, or a human asking what the committed index says.{{end}}

## Act in this order

1. Ask the workspace what exists, with the verb that answers your question:
   `magus describe targets` (every target; `-o name` for bare names),
   `magus ls` (every project with its spell, sources, outputs, depends_on),
   `magus describe spells`, `magus describe projects`.{{if .Full}} These are live, so they
   are right even mid-change, and they take a `-o json` for machine reading.{{end}}

2. Then reach for the verbs. Prefer the MCP tools. At session start, or after an
   MCP call fails, check `magus status --probe=mcp`. If it is unavailable, tell
   the user once that `magus server start` restores the full agent surface, then
   use the CLI equivalent from the same row below. Do not stop or grep.{{if .Full}} CLI
   fallback remains correct, but has no tool discovery or warm daemon graph.{{end}}

   | question                                      | MCP tool        | CLI                                |
   | --------------------------------------------- | --------------- | ---------------------------------- |
   | find and relate entities                      | `magus_query`   | `magus query "<terms>"`            |
   | one node: its edges, provenance, blast radius | `magus_explain` | `magus explain <node>`             |
   | how do two nodes relate                       | `magus_path`    | `magus path <a> <b>`               |
   | where risk concentrates                       | `magus_stats`   | `magus graph stats`                |
   | where a code symbol is defined and used       | `magus_refs`    | `magus refs <symbol>`              |
   | what a branch changed in the graph            | (export + diff) | `magus graph diff <baseline.json>` |

   Prefer these over grep and glob for anything in the magus domain. `magus_refs`
   needs a workspace that declares a SCIP index (`knowledge.symbols` in config);{{if .Full}} it
   is the occurrence-shaped def/references answer, so use it over `magus_query` for a
   symbol's fan-in.{{end}} Every empty result carries a verdict: `absent` means magus
   searched every symbol index this workspace declares and the thing is not there;
   `unknown` names the projects it could not search, and building those with
   `magus graph build` is what turns the answer into a fact. Read the verdict before
   concluding anything from an empty result.

{{if .Full}}   The graph relates entities; the evaluated dispatch plan lives one verb over.
{{end}}   `magus describe target <name>` prints, per project, the resolved source globs,
   output globs (the generated files), spells, and policy for that target{{if .Full}} - use it
   when the question is "what feeds or comes out of this target", not "what relates
   to it"{{end}}.

## Rewriting a symbol everywhere it appears

`magus refs <symbol>` answers "where is this used" at file granularity, and its line
list is CAPPED - it describes fan-in, and a rewrite driven off it silently skips sites.
Add `--occurrences` for the edit-precise view: every occurrence, uncapped, with start and
end line/column, and each range checked against the file on disk.

```sh
magus refs <symbol> --occurrences -o json
```

magus reports the sites; YOU apply the edits. It will not rewrite the tree for you, the
same way `magus affected` names what a change reaches without touching it.

**Never drive the rewrite from a pattern** - not `sed -i`, not a scripted
substitute-and-write. A regex cannot tell YOUR symbol from a dependency's symbol of the
same name, and it writes before anyone reads a diff: a `\.Sum\b` rewrite aimed at one
proto field also hits the OTel SDK's `metricdata.Sum` and a histogram's `dp.Sum`. The
index knows which is which. Apply the sites it reports, then let the compiler enumerate
what still moved; widening the pattern until the errors stop is the same mistake with
extra steps.

**A not-indexed project is a stop, not an empty result.** `magus refs` says
`verdict: unknown, not absent` and names the projects it could not see. Run
`magus graph build` and ask again. Reading that verdict as "no matches" and falling back
to a text search is how a rename misses every site in an unindexed project - and a fresh
worktree starts unindexed, so this is the normal state at the moment you most want a
rename.

Three things decide whether the result is usable, and skipping any of them is how a bulk
rewrite corrupts a file:

- **Edit only `verified` sites.** Each occurrence carries a `status`. `verified` means
  magus read that exact range and found the symbol there. `mismatch` means it found
  something else - the index predates an edit - and `unreadable` means the range is no
  longer inside the file. The `text` field shows what is really there, and `names` is every
  spelling that would have verified, so you can check the verdict rather than trust it.
- **Check the exit status when scripting `-o name`.** It emits `file:line:col` for the
  verified sites ONLY, so a wholly stale index prints nothing - which on its own is
  indistinguishable from a symbol that is never used. Exit 1 means sites were found and
  withheld, and the count goes to stderr. Do not read empty output as "nothing to do".
- **Apply back-to-front within each file.** Replacing a name with one of a different
  length shifts every later column on that line, so editing top-down invalidates each
  subsequent range as you go. Walk each file's occurrences in reverse. Files are
  independent of each other.
- **Treat a `stale` file as a stop, not a filter.** A file is marked stale when any of its
  ranges failed to verify, which proves it changed after indexing - so the index may also
  be MISSING occurrences added since, and no per-site check can see a site that is not in
  the list. Re-run that project's `scip` target and ask again. Editing the verified sites
  and skipping the rest produces a half-renamed tree that may still compile.

Completeness rests on the index being current even when everything verifies: an edit that
appended a new use without disturbing existing ranges leaves every site verifying while
adding one magus never saw. `magus status` reports which indexes are fresh. Re-index
first when the tree has moved since you last did, and check the verdict for projects that
declare no index at all - those are not searched.

## Query grammar

Free-text terms (AND) plus field matchers. A matcher is `field<op>value`, and the operators
are `=` (match), `!=` (exclude), `=~` (regex):

- `build` - free text over IDs, labels, and docs
- `kind=spell` - only that node kind
- `project=pkg/foo` - everything the project owns{{if .Full}}: the project node, its
  targets, and the files/functions/docs whose source lives under it (nested
  projects claim their own; the root `.` owns only what no nested project does){{end}}
- `relation=uses` - seed from nodes touching that edge{{if .Full}} (`relation=calls`
  reaches symbol-to-symbol call edges, so it loads the lazy symbol shards){{end}}
- `id=build` - substring match on the node ID
- `kind!=op` - exclude these
- `id=~build$` - regex over the target; `kind=~"spell|op"` ORs the alternatives
- `id=target:*build` - `*` wildcard, matching any run (in a value or a free-text term)
- `"exact phrase"` - keep a quoted span as one term

{{if .Full}}The `:` grammar (`kind:spell`) and dash negation (`-kind:op`) are the pre-`=`
spelling, kept as a compat alias so old invocations still parse. Prefer `=`/`!=`/`=~`.{{else}}The `:`/`-kind:op` spelling still parses (compat); prefer `=`/`!=`/`=~`.{{end}}

A query returns ranked matches plus their neighborhood, bounded by `--budget`
(default 50).{{if .Full}} For a large match set over MCP, pass `limit` and echo the returned
`next_cursor` to fetch the next page.{{else}} Over MCP, page with `limit` plus the returned
`next_cursor`.{{end}}

## Retrieving prose from the docs

Every markdown heading in the workspace is a `docsection` node, so documentation is
QUERYABLE, not something to read whole. When you are looking for WHERE something is
explained - in this repo's docs, a project's README, any tracked markdown - query the
section rather than cat or grep the file:

- `magus query "kind=docsection <terms>"` returns the heading whose section covers your
  terms. Each result's id and Source are `<path>#<anchor>` - a citable pointer to the exact
  passage, the same fragment a link into the rendered page carries. Read that one section,
  not the whole page.
- Scope it with `project=<p>` and combine free-text terms.{{if .Full}} `magus explain
  "docsection:<path>#<anchor>"` shows the page a section belongs to and what it links to; a
  page `contains` its sections and a section contains the headings nested under it, so you
  can walk the outline.{{end}}
- Prose only: a code file is not indexed this way - `magus refs` and the entity kinds above
  still cover code and the domain model.

Reading one file you already know the path of is fine. This replaces the SCAN - the grep or
cat over markdown to find a passage - not a targeted read.

## Reading results

- Reading as a machine? Add `-o json`: every verb returns a stable,
  `schema_version`-stamped OBJECT with a top-level wrapper - key into the
  plural (`.matches`, `.targets`), it is never a bare array. `-o name` prints
  bare IDs for piping. Do not scrape the human text or trim it with `head`.
{{if .Full}}  Over MCP the tools already return structured content; nothing to shape.{{end}}
- Node IDs are stable and structured: `<kind>:<qualified-name>`, e.g.
  `target:pkg/foo:build`, `spell:go`, `diagnostic:MGS2001`. Key on them{{if .Full}}; a rename
  is a delete plus an add{{end}}.
- Edges are directed and carry a `confidence` - `extracted` (read directly off a
  source) or `inferred` (a rubric score) - plus `provenance` (where it came from).
- Node `attrs` surface metadata{{if .Full}}: a project's `engine` and `target_count`, a
  target's inherited `engine`, a doc's `title` and `tags`{{end}}. The `duration_p75_ms`,
  `cache_hit_rate`, `run_samples`, `last_output_ref`, and `last_run_ok` attrs are
  OBSERVED from local run history{{if .Full}}, not derived from sources - read them as history, not
  guarantees{{end}}. A target's `last_output_ref` is the `refxxxxxxxx` id of its most recent
  captured run (with `last_run_ok` its `true`/`false` outcome), so `magus query output
<ref>` on it fetches that output{{if .Full}} - a target-to-output hop{{end}}. When `knowledge.vcs` is
  enabled, file nodes also carry `vcs_last_commit`, `vcs_last_modified`, and
  `vcs_commits` extracted from git history.
- Every output carries `schema_version`; a bump means the node/edge shape changed.

## Ownership and blast radius

If the repo commits a `CODEOWNERS` file, the graph has `owner` nodes with `owns`
edges to the projects and files they cover.{{if .Full}} Combine that with dependency edges to
answer "who owns the blast radius of this change": `magus explain <node>` for the
node's owners and dependents, or `magus query kind=owner` to list owners. Only
declared CODEOWNERS ownership appears - it is not blame-inferred.{{else}} `magus explain
<node>` for owners plus dependents; `magus query kind=owner` to list. Declared
ownership only, never blame-inferred.{{end}}

## Across workspaces and neighbors

- `--global` unions every workspace registered in config
  (`knowledge.workspaces`); IDs are namespaced per workspace (`web//spell:go`).
- `magus affected`, `magus_insight`, and `magus describe` sit alongside the graph;
  `magus graph export -o json` dumps the whole graph for bulk analysis.
- To show a PR's domain impact, run `magus graph diff --rev main -o markdown` for a CI
  comment{{if .Full}} (nodes/edges added, removed, or changed); `--rev` builds the base graph from
  that revision's files, or pass a `graph export -o json` baseline file instead{{end}}.

## Do not render the graph yourself

magus emits; it does not render. To LOOK at the graph, do not draw it: OFFER the
human an export - `magus graph export -o json` (or `-o graphml`) opens directly in
Gephi, yEd, or a browser graph tool.{{if .Full}} The emit-never-render rule that governs magus
governs you too.{{end}}

## Fetching current behavior

For flags and behavior this skill does not cover, run any verb with `-h`, and read
the magus documentation site.{{if .Full}} Prefer the tools' own output over assumptions.{{end}}
