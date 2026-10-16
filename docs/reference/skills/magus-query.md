---
title: magus-query
generated_from: internal/agent/skills/magus-query/SKILL.md
description: "Query the magus knowledge graph to find and relate entities (projects, targets, spells, ops, charms, modules, diagnostics, docs)."
tags: [agents, skills, magus-query]
skill_full_bytes: 15719
skill_short_bytes: 9255
---

# magus-query

Query the magus knowledge graph to find and relate entities (projects, targets, spells, ops, charms, modules, diagnostics, docs). Use INSTEAD of Grep or Glob in a repo with magusfile.buzz whenever the question is what exists, what depends on what, where something is used, or how two entities relate - a graph answer is verified against declared sources, a grep hit is a guess.

Install it, rather than copying from this page:

```sh
magus agent install .claude/skills   # writes both forms below
```

An installed copy carries a provenance stamp, so `magus doctor` can tell you when a magus upgrade has made it stale. Text copied from this page carries none.

## What an installed copy carries

`magus agent install` writes this frontmatter above the body. `magus doctor` reads it to report whether your installed skills are current.

| field | value |
| --- | --- |
| `license` | `GPL-3.0-or-later` |
| `compatibility` | `any-agent` |
| `source` | `magus` |
| `agent-skill-version` | `142` |
| `knowledge-schema-version` | `23` |
| `skill-content` | `3929ff69d4a2` |
| `skill-variant` | `full` |

The `skill-content` digest covers this skill alone, and both forms below report it: they go stale together, never one silently, and a change to another skill does not move it.

## The two forms

Both are hand-authored from one source body. The short form is the always-loaded primary - the enumeration dropped, the judgment kept, for the most capable readers rather than the least. The full form is its `<name>-full` twin, loaded by name when a reader wants the rationale. The bar above shows how much shorter the primary is; switch between them here to see exactly what it gave up. See [Skills](../../guides/integrations/agents/skills.md) for how to choose.

<article class="landing-tabs">
<header>
<input type="radio" name="magus-query-variant" id="magus-query-tab-short" checked>
<label for="magus-query-tab-short">Short form</label>
<input type="radio" name="magus-query-variant" id="magus-query-tab-full">
<label for="magus-query-tab-full">Full form</label>
</header>

<section class="landing-tabpanel">

```sh
magus agent install --tar | tar -xO -f - magus-query/SKILL.md
```

````markdown
# magus knowledge graph

magus keeps deterministic, cache-backed graph of its own domain; query it to find and relate entities, not grep source.

Fast path: in magus workspace (magusfile.buzz at root), ask graph FIRST: "what exists", "what depends on X", "where is Y used", "how do A and B relate".
Never open Grep or Glob for it.
If graph cannot answer -> say so, then fall back; silent fallback hides gap.
Lookup whose answer is all you need -> `magus-scout` agent, where harness installed one; otherwise worker on host's cheapest model.

`MAGUS.md` is NOT your source: generated index for humans, true only as of last regeneration.
Read it only as last resort: no server AND no CLI, or human asking what committed index says.

## Order

1. Ask workspace what exists, with verb answering your question:
   - `magus describe targets`: every target; `-o name` for bare names.
   - `magus ls`: every project with spell, sources, outputs, depends_on.
   - `magus describe spells`, `magus describe projects`.

2. Then verbs. Prefer MCP tool when host exposes it.
   - Call tool itself to check availability; `magus status --probe=mcp` tests loopback HTTP listener -> can fail while stdio or Unix-socket MCP works.
   - If tool missing or call fails -> CLI equivalent from same row below. Do not stop or grep.
   - `magus status --probe=readiness`: checks workspace loaded on server socket; does not test host MCP registration.
   - Never start server only to unlock tool.

   Question: MCP; CLI
   - find and relate entities: `client` (`magus\query`); `magus query "<terms>"`
   - one node: edges, provenance, blast radius: `client` (`magus\explain`); `magus explain <node>`
   - how two nodes relate: `client` (`magus\path`); `magus path <a> <b>`
   - where risk concentrates: `client` (`magus\stats`); `magus graph stats`
   - where code symbol defined and used: `client` (`magus\refs`); `magus refs <symbol>`
   - what branch changed in graph: (export + diff); `magus graph diff <baseline.json>`

   Prefer these over grep and glob for anything in magus domain.
   `magus\refs` needs workspace declaring SCIP index (`knowledge.symbols` in config).

   Every empty result carries verdict; read it before concluding anything:
   - `absent`: magus searched every symbol index workspace declares; thing not there.
   - `unknown`: names projects it could not search; build those with `magus graph build` -> answer becomes fact.

   Result can name own next step, real ids filled in: `next:` label in text, `next` field in JSON. Suggestion, never order.

   `magus describe target <name>`: per project, resolved source globs, output globs (generated files), spells, policy for that target.

## Rewriting symbol everywhere

`magus refs <symbol>`: "where is this used" at file granularity. Line list CAPPED -> rewrite driven off it silently skips sites.
`--occurrences`: edit-precise view: every occurrence, uncapped, start and end line/column; each range checked against file on disk.

```sh
magus refs <symbol> --occurrences -o json
```

magus reports sites; YOU apply edits. It never rewrites tree.

Never drive rewrite from pattern: not `sed -i`, not scripted substitute-and-write.
- Regex cannot tell YOUR symbol from dependency's same-named symbol, and writes before anyone reads diff.
- Index knows which is which. Apply reported sites, then let compiler enumerate what still moved. Widening pattern until errors stop: same mistake.

Not-indexed project is stop, not empty result: `magus refs` says `verdict: unknown, not absent`, names projects it could not see -> run `magus graph build`, ask again.
Fresh worktree starts unindexed: normal state when you most want rename.

Three things decide whether result usable; skipping any corrupts file in bulk rewrite:
- Edit only `verified` sites. Each occurrence carries `status`. `verified`: magus read that exact range, found symbol. `mismatch`, `unreadable`: index predates edit; `text` and `names` let you check.
- Check exit status when scripting `-o name`: emits `file:line:col` for verified sites ONLY. Wholly stale index prints nothing, which alone looks like unused symbol. Exit 1: sites found and withheld, count on stderr.
- Apply back-to-front within each file: different-length replacement shifts every later column on that line. Walk each file's occurrences in reverse; files independent.
- `stale` file is stop, not filter. File stale when any of its ranges failed to verify; index may then also be MISSING occurrences added since, which no per-site check can see. Re-run project's `scip` target, ask again.

Completeness rests on current index even when everything verifies: newly appended use verifies nothing, never seen.
- `magus status` reports which indexes fresh. Re-index first when tree moved since last index.
- Check verdict for projects declaring no index at all; those not searched.

## Query grammar

Free-text terms plus field matchers. Matchers filter; free text RANKS. Matcher: `field<op>value`; operators `=` (match), `!=` (exclude), `=~` (regex):
- `build`: free text ranked by BM25F over names, signatures, paths, docs; any one word matches; fewer words than the query = PARTIAL match
- `internal/trail`: term holding `/` must appear whole
- `kind=spell`: only that node kind
- `project=pkg/foo`: everything project owns
- `relation=uses`: seed from nodes touching that edge
- `id=build`: substring match on node ID
- `kind!=op`: exclude these
- `id=~build$`: regex over target; `kind=~"spell|op"` ORs alternatives
- `id=target:*build`: `*` wildcard, matching any run (in value or free-text term)
- `"exact phrase"`: quoted span as one term

`:`/`-kind:op` spelling still parses (compat); prefer `=`/`!=`/`=~`.
Query returns ranked matches plus neighborhood, bounded by `--budget` (default 50).
Over MCP: page with `{limit, offset}` in `magus\query` options; `matchCount` stays total.

## Prose from docs

Every Markdown heading in workspace is `docsection` node -> docs QUERYABLE, not read whole.
WHERE something is explained: query section; do not cat or grep file.
- `magus query "kind=docsection <terms>"`: heading whose section covers terms. Each result's id and Source: `<path>#<anchor>`, citable pointer to exact passage. Read that one section, not whole page.
- Scope with `project=<p>`; combine free-text terms.
- Prose only: code files not indexed this way. `magus refs` and entity kinds above cover code and domain model.

Reading one file whose path you know: fine. This replaces SCAN, not targeted read.

## Reading results

- Machine reading -> `-o json`. Every verb returns stable, `schema_version`-stamped OBJECT with top-level wrapper. Key into plural (`.matches`, `.targets`); never bare array. `-o name`: bare IDs for piping. Do not scrape human text or trim with `head`.
- Each match carries `why`: `matched`/`of` words held (partial matches labeled), `terms` per field, `lists` ranks, `seeds` (changed files, lease paths). `ranking.weights`: weight file digest. explain/path/refs take full matches only.
- Node IDs stable, structured: `<kind>:<qualified-name>`, like `target:pkg/foo:build`, `spell:go`, `diagnostic:MGS2001`. Key on them.
- Edges directed, carry `confidence`: `extracted` (read directly off source) or `inferred` (rubric score), plus `provenance` (origin).
- Node `attrs` surface metadata.
  - `duration_p75_ms`, `cache_hit_rate`, `run_samples`, `last_output_ref`, `last_run_ok`: OBSERVED from local run history.
  - `magus query output <ref>` on target's `last_output_ref` fetches latest captured run.
- Every output carries `schema_version`; bump means node/edge shape changed.

## Ownership, blast radius

If repo commits `CODEOWNERS` file -> graph has `owner` nodes with `owns` edges to projects and files they cover.
`magus explain <node>`: owners plus dependents; `magus query kind=owner` lists them.
Ownership declared only, never blame-inferred.

## Other sessions' work here

Agents before you left record, where workspace declares session adapter:
- `magus explain <node>` ends with `agent sessions:` line when any loaded session touched that file. Silence: nothing touched it.
- `magus session` lists sessions; `magus session show <id>` opens one, joined against this checkout's guard trail.

Read it before non-trivial edit: file last committed weeks ago may have been rewritten yesterday by session whose work is not committed. Refused write there: guard rule you are about to hit too.
Never infer from empty result that nobody worked on file: equally means workspace declares no adapter, common case.

## Across workspaces, neighbors

- `--global` unions every workspace registered in config (`knowledge.workspaces`); IDs namespaced per workspace (`web//spell:go`).
- `magus affected`, `client` (`magus\insight`), `magus describe` sit beside graph. `magus graph export -o json` dumps whole graph for bulk analysis.
- PR domain impact: `magus graph diff --rev main -o markdown` for CI comment.

## Do not render graph yourself

magus emits; does not render. To LOOK at graph, do not draw it: OFFER human export.
`magus graph export -o json` (or `-o graphml`) opens in Gephi, yEd, or browser graph tool.

## Current behavior

Flags and behavior this skill does not cover: run any verb with `-h`, read magus documentation site.
````


</section>

<section class="landing-tabpanel">

```sh
magus agent install --tar | tar -xO -f - magus-query-full/SKILL.md
```

````markdown
# magus knowledge graph

magus keeps a deterministic, cache-backed graph of its own domain. Query it to find
and relate entities instead of grepping source. This skill teaches HOW to use
the tools; the verbs below say WHAT is in this specific workspace. The division is
strict, so this skill never goes stale when a workspace changes, only when the
tool set does.

FAST PATH: in a magus workspace (a magusfile.buzz at the root), ask the graph FIRST.
That covers "what exists", "what depends on X", "where is Y used", and "how do A and
B relate". Do not open Grep or Glob for it. Unlike a
grep hit, a graph answer is verified: every edge is extracted from a declared
source or scored by a rubric, and says which. If the graph cannot answer, say so,
then fall back: a silent fallback hides the gap that should be
reported.

Send a lookup whose answer is all you need to the `magus-scout` agent, where your
harness installed one. Otherwise use a worker on your host's cheapest model. Answering
from the graph takes no judgment, so the strongest model spends its tokens for nothing
there, and a scout's short context keeps every lookup cheap.

`MAGUS.md` IS NOT YOUR SOURCE. It is a generated routing index written for a
HUMAN reading the repo, and it is only as true as its last regeneration: a
workspace whose generate target has not run since the last change describes a
tree that no longer exists. Every fact in it has a live command that cannot be
stale, and those commands scope to a project where the file covers the whole
workspace. Read it as a LAST RESORT: when no server is reachable and the CLI is
unavailable too, or when a human explicitly asks what the committed index says.

## Contents

- Act in this order
- Rewriting a symbol everywhere it appears
- Query grammar
- Retrieving prose from the docs
- Reading results
- Ownership and blast radius
- What other sessions already did here
- Across workspaces and neighbors
- Do not render the graph yourself
- Fetching current behavior

## Act in this order

1. Ask the workspace what exists, with the verb that answers your question:
   - `magus describe targets`: every target; `-o name` for bare names.
   - `magus ls`: every project with its spell, sources, outputs, depends_on.
   - `magus describe spells`, `magus describe projects`.

   These are live, so they
   are right even mid-change, and they take a `-o json` for machine reading.

2. Then reach for the verbs. Prefer an MCP tool when this host exposes it.
   - Call the tool itself to check availability. `magus status --probe=mcp` tests
     the loopback HTTP listener, so it can fail while stdio or Unix-socket MCP
     works.
   - If the tool is missing or its call fails, use the CLI equivalent from the same
     row below. Do not stop or grep.
   - `magus status --probe=readiness` checks that this workspace is loaded on the
     server socket. It does not test the host's MCP registration.
   - Never start a server only to unlock a tool. CLI
     fallback remains correct, but has no tool discovery or warm server graph.

   | question                                      | MCP                                              | CLI                                |
   | --------------------------------------------- | ------------------------------------------------ | ---------------------------------- |
   | find and relate entities                      | `client` (`magus\query`)         | `magus query "<terms>"`            |
   | one node: its edges, provenance, blast radius | `client` (`magus\explain`)       | `magus explain <node>`             |
   | how do two nodes relate                       | `client` (`magus\path`)          | `magus path <a> <b>`               |
   | where risk concentrates                       | `client` (`magus\stats`)         | `magus graph stats`                |
   | where a code symbol is defined and used       | `client` (`magus\refs`)          | `magus refs <symbol>`              |
   | what a branch changed in the graph            | (export + diff) | `magus graph diff <baseline.json>` |

   Prefer these over grep and glob for anything in the magus domain. `magus\refs`
   needs a workspace that declares a SCIP index (`knowledge.symbols` in config). It
   is the occurrence-shaped def/references answer, so use it over `magus\query` for a
   symbol's fan-in.

   Every empty result carries a verdict. Read it before concluding anything:
   - `absent`: magus searched every symbol index this workspace declares, and the
     thing is not there.
   - `unknown`: it names the projects it could not search. Build those with
     `magus graph build` to turn the answer into a fact.

   A result can name its own next step, with real ids filled in. Text
   mode prints the commands under a `next:` label; `-o json` carries a `next` field.
   Following one is optional: a suggestion, never an order. A result with nothing
   to suggest has no `next`.

   The graph relates entities; the evaluated dispatch plan lives one verb over.
   `magus describe target <name>` prints, per project, the resolved source globs,
   output globs (the generated files), spells, and policy for that target; use it
   when the question is "what feeds or comes out of this target", not "what relates
   to it".

## Rewriting a symbol everywhere it appears

`magus refs <symbol>` answers "where is this used" at file granularity. Its line list
is CAPPED, so a rewrite driven off it silently skips sites. Add `--occurrences` for the
edit-precise view: every occurrence, uncapped, with start and end line/column. Each
range is checked against the file on disk.

```sh
magus refs <symbol> --occurrences -o json
```

magus reports the sites; YOU apply the edits. It never rewrites the tree, the same way
`magus affected` names what a change reaches without touching it.

**Never drive the rewrite from a pattern**: not `sed -i`, not a scripted
substitute-and-write.

- A regex cannot tell YOUR symbol from a dependency's symbol of the same name, and
  it writes before anyone reads a diff. A `\.Sum\b` rewrite aimed at one proto
  field also hits the OTel SDK's `metricdata.Sum` and a histogram's `dp.Sum`.
- The index knows which is which. Apply the sites it reports, then let the compiler
  enumerate what still moved. Widening the pattern until the errors stop is the
  same mistake.

**A not-indexed project is a stop, not an empty result.** `magus refs` says
`verdict: unknown, not absent` and names the projects it could not see. Run
`magus graph build` and ask again. Reading that verdict as "no matches" and falling
back to text search misses every site in an unindexed project. A fresh worktree
starts unindexed, so this is the normal state when you most want a rename.

Three things decide whether the result is usable. Skipping any of them is how a bulk
rewrite corrupts a file:

- **Edit only `verified` sites.** Each occurrence carries a `status`. `verified`
  means magus read that exact range and found the symbol there. `mismatch` means it
  found something else (the index predates an edit); `unreadable` means the range
  is no longer inside the file. `text` shows what is there, and `names` lists every
  spelling that would have verified, so you can check the verdict.
- **Check the exit status when scripting `-o name`.** It emits `file:line:col` for
  verified sites ONLY. A wholly stale index prints nothing, which alone looks like an
  unused symbol. Exit 1 means sites were found and withheld, with the count on stderr.
- **Apply back-to-front within each file.** A replacement of a different length
  shifts every later column on that line. Walk each file's occurrences in reverse;
  files are independent.
- **Treat a `stale` file as a stop, not a filter.** A file is stale when any of its
  ranges failed to verify. The index may then also be MISSING occurrences added
  since, which no per-site check can see. Re-run that project's `scip` target and ask
  again. Editing only the verified sites yields
  a half-renamed tree that may still compile.

Completeness rests on a current index even when everything verifies. An edit that
appended a new use without disturbing existing ranges leaves every site verifying
while adding one magus never saw.

- `magus status` reports which indexes are fresh. Re-index first when the tree has
  moved since you last did.
- Check the verdict for projects that declare no index at all; those are not
  searched.

## Query grammar

Free-text terms plus field matchers. Matchers filter; free text RANKS. A matcher is
`field<op>value`; the operators are `=` (match), `!=` (exclude), `=~` (regex):

- `build`: free text, ranked by BM25F over names, signatures, paths and docs. A match
  needs any one word; one holding fewer words than the query is a PARTIAL match
- `internal/trail`: a term holding `/` must appear whole
- `kind=spell`: only that node kind
- `project=pkg/foo`: everything the project owns: the project node, its
  targets, and the files/functions/docs whose source lives under it (nested
  projects claim their own; the root `.` owns only what no nested project does)
- `relation=uses`: seed from nodes touching that edge (`relation=calls`
  reaches symbol-to-symbol call edges, so it loads the lazy symbol shards)
- `id=build`: substring match on the node ID
- `kind!=op`: exclude these
- `id=~build$`: regex over the target; `kind=~"spell|op"` ORs the alternatives
- `id=target:*build`: `*` wildcard, matching any run (in a value or a free-text term)
- `"exact phrase"`: keep a quoted span as one term

The `:` grammar (`kind:spell`) and dash negation (`-kind:op`) are the pre-`=`
spelling, kept as a compat alias so old invocations still parse. Prefer `=`/`!=`/`=~`.

A query returns ranked matches plus their neighborhood, bounded by `--budget`
(default 50). For a large match set over MCP, pass `{limit, offset}` in the
options of `magus\query` and raise `offset` by `limit` for the next page; `matchCount`
stays the total, so you know when you have them all.

## Retrieving prose from the docs

Every Markdown heading in the workspace is a `docsection` node, so documentation is
QUERYABLE, not something to read whole. To find WHERE something is explained, query
the section; do not cat or grep the file:

- `magus query "kind=docsection <terms>"` returns the heading whose section covers
  your terms. Each result's id and Source are `<path>#<anchor>`, a citable pointer
  to the exact passage. Read that one section, not the whole page.
- Scope it with `project=<p>` and combine free-text terms. `magus explain
  "docsection:<path>#<anchor>"` shows the page a section belongs to and what it links to; a
  page `contains` its sections and a section contains the headings nested under it, so you
  can walk the outline.
- Prose only: code files are not indexed this way. `magus refs` and the entity
  kinds above cover code and the domain model.

Reading one file whose path you know is fine. This replaces the SCAN, not a targeted
read: grep or cat over Markdown to find a passage. The docs covered are
this repo's docs, a project's README, and any tracked Markdown, and the anchor is the
same fragment a link into the rendered page carries.

## Reading results

- Reading as a machine? Add `-o json`. Every verb returns a stable,
  `schema_version`-stamped OBJECT with a top-level wrapper. Key into the plural
  (`.matches`, `.targets`); it is never a bare array. `-o name` prints bare IDs for
  piping. Do not scrape the human text or trim it with `head`.
  Over MCP the tools already return structured content; nothing to shape.
- Each match carries `why`: `matched` of `of` says how many of your words it holds,
  so a partial match is labeled, not hidden; `terms` scores each word per field; `lists`
  gives its rank in each fused list; `seeds` names the changed files or lease paths that
  lifted it. `ranking.weights` is the weight file's digest. `explain`, `path` and `refs`
  take only full matches, so a name nothing holds whole answers absent.
- Node IDs are stable and structured: `<kind>:<qualified-name>`, e.g.
  `target:pkg/foo:build`, `spell:go`, `diagnostic:MGS2001`. Key on them; a rename
  is a delete plus an add.
- Edges are directed and carry a `confidence`: `extracted` (read directly off a
  source) or `inferred` (a rubric score), plus `provenance` (where it came from).
- Node `attrs` surface metadata: a project's `engine` and `target_count`, a
  target's inherited `engine`, a doc's `title` and `tags`.
  - `duration_p75_ms`, `cache_hit_rate`, `run_samples`, `last_output_ref`, and
    `last_run_ok` are OBSERVED from local run history, not derived from sources; read them as history, not
    guarantees.
  - `magus query output <ref>` on a target's `last_output_ref` fetches its latest
    captured run (a target-to-output hop). The ref is a `refxxxxxxxx` id, and
    `last_run_ok` is that run's `true`/`false` outcome.
  - With `knowledge.vcs` enabled, file nodes also carry `vcs_last_commit`,
    `vcs_last_modified`, and `vcs_commits` from git history.
- Every output carries `schema_version`; a bump means the node/edge shape changed.

## Ownership and blast radius

If the repo commits a `CODEOWNERS` file, the graph has `owner` nodes with `owns`
edges to the projects and files they cover. Combine that with dependency edges to
answer "who owns the blast radius of this change": `magus explain <node>` for the
node's owners and dependents, or `magus query kind=owner` to list owners. Only
declared CODEOWNERS ownership appears; it is not blame-inferred.

## What other sessions already did here

Agents before you left a record. Where a workspace declares a session adapter,
`magus graph build` folds each host's transcripts into a local store, and
`magus explain` and `magus session` read it back:

- `magus explain <node>` ends with an `agent sessions:` line when any loaded session
  touched that file: reads, writes, distinct sessions, how long ago, and any write
  the host refused. Silence means nothing touched it.
- `magus session` lists those sessions; `magus session show <id>` opens one, joined
  against this checkout's guard trail.

Read it BEFORE a non-trivial edit, for the reason the git half of the same output
exists. A file last committed three weeks ago looks dormant and may have been
rewritten twice yesterday by a session whose work is not committed yet. Four
sessions on one file is a reason to look at what they did before adding a fifth
opinion, and a refused write is a guard rule you are about to hit too.

Never infer from an empty result that nobody worked on a file. It equally means the
workspace declares no adapter, the common case.

## Across workspaces and neighbors

- `--global` unions every workspace registered in config (`knowledge.workspaces`);
  IDs are namespaced per workspace (`web//spell:go`).
- `magus affected`, `client` (`magus\insight`), and `magus describe` sit beside the
  graph. `magus graph export -o json` dumps the whole graph for bulk analysis.
- To show a PR's domain impact, run `magus graph diff --rev main -o markdown` for a
  CI comment (nodes/edges added, removed, or changed); `--rev` builds the base graph from
  that revision's files, or pass a `graph export -o json` baseline file instead.

## Do not render the graph yourself

magus emits; it does not render. To LOOK at the graph, do not draw it: OFFER the
human an export. `magus graph export -o json` (or `-o graphml`) opens in Gephi, yEd,
or a browser graph tool. The emit-never-render convention that governs magus
governs you too.

## Fetching current behavior

For flags and behavior this skill does not cover, run any verb with `-h` and read the
magus documentation site. Prefer the tools' own output over assumptions.
````


</section>

</article>
