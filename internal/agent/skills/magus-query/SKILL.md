# magus knowledge graph

magus keeps a deterministic, cache-backed graph of its own domain. Query it to find
and relate entities instead of grepping source.{{if .Full}} This skill teaches HOW to use
the tools; the verbs below say WHAT is in this specific workspace. The division is
strict, so this skill never goes stale when a workspace changes, only when the
tool surface does.{{end}}

FAST PATH: in a magus workspace (a magusfile.buzz at the root), ask the graph FIRST.
That covers "what exists", "what depends on X", "where is Y used", and "how do A and
B relate". Do not open Grep or Glob for it.{{if .Full}} Unlike a
grep hit, a graph answer is verified: every edge is extracted from a declared
source or scored by a rubric, and says which.{{end}} If the graph cannot answer, say so,
then fall back{{if .Full}}: a silent fallback hides the gap that should be
reported{{else}}: falling back silently hides the gap{{end}}.

`MAGUS.md` IS NOT YOUR SOURCE.{{if .Full}} It is a generated routing index written for a
HUMAN reading the repo, and it is only as true as its last regeneration: a
workspace whose generate target has not run since the last change describes a
tree that no longer exists. Every fact in it has a live command that cannot be
stale, and those commands scope to a project where the file covers the whole
workspace. Read it as a LAST RESORT: when no server is reachable and the CLI is
unavailable too, or when a human explicitly asks what the committed index says.{{else}} It is a
generated index for humans, true only as of its last regeneration. Read it only as a
last resort: no server AND no CLI, or a human asking what the committed index says.{{end}}

## Act in this order

1. Ask the workspace what exists, with the verb that answers your question:
   - `magus describe targets`: every target; `-o name` for bare names.
   - `magus ls`: every project with its spell, sources, outputs, depends_on.
   - `magus describe spells`, `magus describe projects`.{{if .Full}}

   These are live, so they
   are right even mid-change, and they take a `-o json` for machine reading.{{end}}

2. Then reach for the verbs. Prefer an MCP tool when this host exposes it.
   - Call the tool itself to check availability. `magus status --probe=mcp` tests
     the loopback HTTP listener, so it can fail while stdio or Unix-socket MCP
     works.
   - If the tool is missing or its call fails, use the CLI equivalent from the same
     row below. Do not stop or grep.
   - `magus status --probe=readiness` checks that this workspace is loaded on the
     server socket. It does not test the host's MCP registration.
   - Never start a server only to unlock a tool.{{if .Full}} CLI
     fallback remains correct, but has no tool discovery or warm server graph.{{end}}

   | question                                      | MCP                                              | CLI                                |
   | --------------------------------------------- | ------------------------------------------------ | ---------------------------------- |
   | find and relate entities                      | `{{tool "client"}}` (`{{buzz "query"}}`)         | `magus query "<terms>"`            |
   | one node: its edges, provenance, blast radius | `{{tool "client"}}` (`{{buzz "explain"}}`)       | `magus explain <node>`             |
   | how do two nodes relate                       | `{{tool "client"}}` (`{{buzz "path"}}`)          | `magus path <a> <b>`               |
   | where risk concentrates                       | `{{tool "client"}}` (`{{buzz "stats"}}`)         | `magus graph stats`                |
   | where a code symbol is defined and used       | `{{tool "client"}}` (`{{buzz "refs"}}`)          | `magus refs <symbol>`              |
   | what a branch changed in the graph            | (export + diff) | `magus graph diff <baseline.json>` |

   Prefer these over grep and glob for anything in the magus domain. `{{buzz "refs"}}`
   needs a workspace that declares a SCIP index (`knowledge.symbols` in config).{{if .Full}} It
   is the occurrence-shaped def/references answer, so use it over `{{buzz "query"}}` for a
   symbol's fan-in.{{end}}

   Every empty result carries a verdict. Read it before concluding anything:
   - `absent`: magus searched every symbol index this workspace declares, and the
     thing is not there.
   - `unknown`: it names the projects it could not search. Build those with
     `magus graph build` to turn the answer into a fact.

   A result can name its own next step, with real ids filled in{{if .Full}}. Text
   mode prints the commands under a `next:` label; `-o json` carries a `next` field.
   Following one is optional: a suggestion, never an order. A result with nothing
   to suggest has no `next`{{else}}: a `next:` label in text, a `next` field in JSON.
   It is a suggestion, never an order{{end}}.

{{if .Full}}   The graph relates entities; the evaluated dispatch plan lives one verb over.
{{end}}   `magus describe target <name>` prints, per project, the resolved source globs,
   output globs (the generated files), spells, and policy for that target{{if .Full}}; use it
   when the question is "what feeds or comes out of this target", not "what relates
   to it"{{end}}.

## Rewriting a symbol everywhere it appears

`magus refs <symbol>` answers "where is this used" at file granularity. Its line list
is CAPPED, so a rewrite driven off it silently skips sites. Add `--occurrences` for the
edit-precise view: every occurrence, uncapped, with start and end line/column. Each
range is checked against the file on disk.

```sh
magus refs <symbol> --occurrences -o json
```

magus reports the sites; YOU apply the edits. It never rewrites the tree{{if .Full}}, the same way
`magus affected` names what a change reaches without touching it{{end}}.

**Never drive the rewrite from a pattern**: not `sed -i`, not a scripted
substitute-and-write.

- A regex cannot tell YOUR symbol from a dependency's symbol of the same name, and
  it writes before anyone reads a diff.{{if .Full}} A `\.Sum\b` rewrite aimed at one proto
  field also hits the OTel SDK's `metricdata.Sum` and a histogram's `dp.Sum`.{{end}}
- The index knows which is which. Apply the sites it reports, then let the compiler
  enumerate what still moved. Widening the pattern until the errors stop is the
  same mistake.

**A not-indexed project is a stop, not an empty result.** `magus refs` says
`verdict: unknown, not absent` and names the projects it could not see. Run
`magus graph build` and ask again.{{if .Full}} Reading that verdict as "no matches" and falling
back to text search misses every site in an unindexed project.{{end}} A fresh worktree
starts unindexed, so this is the normal state when you most want a rename.

Three things decide whether the result is usable. Skipping any of them is how a bulk
rewrite corrupts a file:

- **Edit only `verified` sites.** Each occurrence carries a `status`. `verified`
  means magus read that exact range and found the symbol there.{{if .Full}} `mismatch` means it
  found something else (the index predates an edit); `unreadable` means the range
  is no longer inside the file. `text` shows what is there, and `names` lists every
  spelling that would have verified, so you can check the verdict.{{else}} `mismatch` and
  `unreadable` mean the index predates an edit; `text` and `names` let you check.{{end}}
- **Check the exit status when scripting `-o name`.** It emits `file:line:col` for
  verified sites ONLY. A wholly stale index prints nothing, which alone looks like an
  unused symbol. Exit 1 means sites were found and withheld, with the count on stderr.
- **Apply back-to-front within each file.** A replacement of a different length
  shifts every later column on that line. Walk each file's occurrences in reverse;
  files are independent.
- **Treat a `stale` file as a stop, not a filter.** A file is stale when any of its
  ranges failed to verify. The index may then also be MISSING occurrences added
  since, which no per-site check can see. Re-run that project's `scip` target and ask
  again{{if .Full}}. Editing only the verified sites yields
  a half-renamed tree that may still compile{{end}}.

Completeness rests on a current index even when everything verifies{{if .Full}}. An edit that
appended a new use without disturbing existing ranges leaves every site verifying
while adding one magus never saw{{else}}: a newly appended use verifies nothing and is
never seen{{end}}.

- `magus status` reports which indexes are fresh. Re-index first when the tree has
  moved since you last did.
- Check the verdict for projects that declare no index at all; those are not
  searched.

## Query grammar

Free-text terms (AND) plus field matchers. A matcher is `field<op>value`; the
operators are `=` (match), `!=` (exclude), `=~` (regex):

- `build`: free text over IDs, labels, and docs
- `kind=spell`: only that node kind
- `project=pkg/foo`: everything the project owns{{if .Full}}: the project node, its
  targets, and the files/functions/docs whose source lives under it (nested
  projects claim their own; the root `.` owns only what no nested project does){{end}}
- `relation=uses`: seed from nodes touching that edge{{if .Full}} (`relation=calls`
  reaches symbol-to-symbol call edges, so it loads the lazy symbol shards){{end}}
- `id=build`: substring match on the node ID
- `kind!=op`: exclude these
- `id=~build$`: regex over the target; `kind=~"spell|op"` ORs the alternatives
- `id=target:*build`: `*` wildcard, matching any run (in a value or a free-text term)
- `"exact phrase"`: keep a quoted span as one term

{{if .Full}}The `:` grammar (`kind:spell`) and dash negation (`-kind:op`) are the pre-`=`
spelling, kept as a compat alias so old invocations still parse. Prefer `=`/`!=`/`=~`.{{else}}The `:`/`-kind:op` spelling still parses (compat); prefer `=`/`!=`/`=~`.{{end}}

A query returns ranked matches plus their neighborhood, bounded by `--budget`
(default 50).{{if .Full}} For a large match set over MCP, pass `{limit, offset}` in the
options of `{{buzz "query"}}` and raise `offset` by `limit` for the next page; `matchCount`
stays the total, so you know when you have them all.{{else}} Over MCP, page with `{limit, offset}`
in `{{buzz "query"}}`'s options; `matchCount` stays the total.{{end}}

## Retrieving prose from the docs

Every Markdown heading in the workspace is a `docsection` node, so documentation is
QUERYABLE, not something to read whole. To find WHERE something is explained, query
the section; do not cat or grep the file:

- `magus query "kind=docsection <terms>"` returns the heading whose section covers
  your terms. Each result's id and Source are `<path>#<anchor>`, a citable pointer
  to the exact passage. Read that one section, not the whole page.
- Scope it with `project=<p>` and combine free-text terms.{{if .Full}} `magus explain
  "docsection:<path>#<anchor>"` shows the page a section belongs to and what it links to; a
  page `contains` its sections and a section contains the headings nested under it, so you
  can walk the outline.{{end}}
- Prose only: code files are not indexed this way. `magus refs` and the entity
  kinds above cover code and the domain model.

Reading one file whose path you know is fine. This replaces the SCAN, not a targeted
read{{if .Full}}: grep or cat over Markdown to find a passage. The docs covered are
this repo's docs, a project's README, and any tracked Markdown, and the anchor is the
same fragment a link into the rendered page carries{{end}}.

## Reading results

- Reading as a machine? Add `-o json`. Every verb returns a stable,
  `schema_version`-stamped OBJECT with a top-level wrapper. Key into the plural
  (`.matches`, `.targets`); it is never a bare array. `-o name` prints bare IDs for
  piping. Do not scrape the human text or trim it with `head`.
{{if .Full}}  Over MCP the tools already return structured content; nothing to shape.{{end}}
- Node IDs are stable and structured: `<kind>:<qualified-name>`, e.g.
  `target:pkg/foo:build`, `spell:go`, `diagnostic:{{mgs "MGS2001"}}`. Key on them{{if .Full}}; a rename
  is a delete plus an add{{end}}.
- Edges are directed and carry a `confidence`: `extracted` (read directly off a
  source) or `inferred` (a rubric score), plus `provenance` (where it came from).
- Node `attrs` surface metadata{{if .Full}}: a project's `engine` and `target_count`, a
  target's inherited `engine`, a doc's `title` and `tags`{{end}}.
  - `duration_p75_ms`, `cache_hit_rate`, `run_samples`, `last_output_ref`, and
    `last_run_ok` are OBSERVED from local run history{{if .Full}}, not derived from sources; read them as history, not
    guarantees{{end}}.
  - `magus query output <ref>` on a target's `last_output_ref` fetches its latest
    captured run{{if .Full}} (a target-to-output hop). The ref is a `refxxxxxxxx` id, and
    `last_run_ok` is that run's `true`/`false` outcome{{end}}.{{if .Full}}
  - With `knowledge.vcs` enabled, file nodes also carry `vcs_last_commit`,
    `vcs_last_modified`, and `vcs_commits` from git history.{{end}}
- Every output carries `schema_version`; a bump means the node/edge shape changed.

## Ownership and blast radius

If the repo commits a `CODEOWNERS` file, the graph has `owner` nodes with `owns`
edges to the projects and files they cover.{{if .Full}} Combine that with dependency edges to
answer "who owns the blast radius of this change": `magus explain <node>` for the
node's owners and dependents, or `magus query kind=owner` to list owners. Only
declared CODEOWNERS ownership appears; it is not blame-inferred.{{else}} `magus explain
<node>` shows owners plus dependents; `magus query kind=owner` lists them. Ownership is
declared only, never blame-inferred.{{end}}

## What other sessions already did here

Agents before you left a record{{if .Full}}. Where a workspace declares a session adapter,
`magus graph build` folds each host's transcripts into a local store, and two
surfaces read it back{{else}}, where the workspace declares a session adapter{{end}}:

- `magus explain <node>` ends with an `agent sessions:` line when any loaded session
  touched that file{{if .Full}}: reads, writes, distinct sessions, how long ago, and any write
  the host refused{{end}}. Silence means nothing touched it.
- `magus session` lists those sessions; `magus session show <id>` opens one, joined
  against this checkout's guard trail.

{{if .Full}}Read it BEFORE a non-trivial edit, for the reason the git half of the same output
exists. A file last committed three weeks ago looks dormant and may have been
rewritten twice yesterday by a session whose work is not committed yet. Four
sessions on one file is a reason to look at what they did before adding a fifth
opinion, and a refused write is a rule you are about to hit too.{{else}}Read it before a non-trivial edit. A file last committed weeks ago may have been
rewritten yesterday by a session whose work is not committed. A refused write
there is a rule you are about to hit too.{{end}}

Never infer from an empty result that nobody worked on a file. It equally means the
workspace declares no adapter, the common case.

## Across workspaces and neighbors

- `--global` unions every workspace registered in config (`knowledge.workspaces`);
  IDs are namespaced per workspace (`web//spell:go`).
- `magus affected`, `{{tool "client"}}` (`{{buzz "insight"}}`), and `magus describe` sit beside the
  graph. `magus graph export -o json` dumps the whole graph for bulk analysis.
- To show a PR's domain impact, run `magus graph diff --rev main -o markdown` for a
  CI comment{{if .Full}} (nodes/edges added, removed, or changed); `--rev` builds the base graph from
  that revision's files, or pass a `graph export -o json` baseline file instead{{end}}.

## Do not render the graph yourself

magus emits; it does not render. To LOOK at the graph, do not draw it: OFFER the
human an export. `magus graph export -o json` (or `-o graphml`) opens in Gephi, yEd,
or a browser graph tool.{{if .Full}} The emit-never-render rule that governs magus
governs you too.{{end}}

## Fetching current behavior

For flags and behavior this skill does not cover, run any verb with `-h` and read the
magus documentation site.{{if .Full}} Prefer the tools' own output over assumptions.{{end}}
