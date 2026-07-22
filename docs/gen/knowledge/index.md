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
magus refs <symbol>         # where an ingested code symbol is defined and referenced
magus graph stats           # god nodes, orphans, doc coverage
magus graph export -o json  # the whole graph as node-link JSON
magus graph diff base.json  # what this branch changed vs an exported baseline
magus graph open            # explore it visually in your browser (data stays local)
```

Prefer a picture? `magus graph open` launches the interactive [Graph
Explorer](console/) seeded with your own workspace - a force-directed, searchable
view of the same graph. Your data never leaves your machine: it rides in the URL
fragment (or a local loopback server with `--serve`), never reaching the site. This
site's own graph is the [live demo](console/).

The committed `MAGUS.md` routing table is the entry point: it lists every node
kind with its count, the query that lists it, and the highest-degree anchor
nodes, so an agent knows what exists before running anything.

## Query grammar

`magus query` takes free-text terms (AND) plus field filters and negation. Terms
are scored with the same leaf-anchored fuzzy match that powers `magus where`.

| Form               | Meaning                                            |
| ------------------ | -------------------------------------------------- |
| `build`            | free text: match node IDs, labels, and docs        |
| `kind:spell`       | only nodes of that kind                            |
| `project:pkg/foo`  | the project node and its targets                   |
| `relation:uses`    | seed from nodes touching a `uses` edge             |
| `id:build`         | substring match on the node ID                     |
| `id:target:*build` | `*` wildcard: matches any run (in a value or term) |
| `-kind:op`         | negation: exclude these                            |
| `"exact phrase"`   | a quoted span stays one term                       |

A query resolves terms to seed nodes, then collects the induced neighborhood up
to a node budget (`--budget`, default 50), so a match on a high-degree node
cannot pull in the whole graph.

## Questions you can ask

Recipes for the graph as a lens on the workspace. Rebuild first with `magus graph
build` if you want it fresh; combine field filters freely.

**What programs does the workspace actually run?** magus owns the task layer, so it
knows the concrete tool behind every operation - not just the source.

```sh
magus query "kind:tool"                    # the workspace's toolchain (go, buf, docker, ...)
magus explain "tool:go"                    # every op and spell that runs go
magus explain "op:go:go-test"              # an op's base argv (the `argv` attr) and its tool
magus path "target:.:test" "tool:go"       # a target reaches its tool via target->op->tool
```

Each spell op carries the base argv it runs on an `argv` attr (rendered with empty
charms) and `use`s the `tool:<program>` node for its argv[0] - the program, its own kind
because it is an entity, not an operation. The op's spell `use`s the tool too, so
`explain tool:go` lists every op and spell that runs go; a target reaches the tool
through its existing `target --uses--> op` edge. (There is no per-target command node:
its argv was always identical to the op's, so the op carries the model.)

So `explain tool:go` lists every op that runs go:

<!-- example:explain-tool-go -->
```console
$ magus explain tool:go
tool:go   tool
tool: go
12 nodes reach this

used by (10)  op:go:go-build, op:go:go-clean, op:go:go-generate,
              op:go:go-mod-tidy, op:go:go-run, op:go:go-test, op:go:go-vet,
              op:go:golangci-lint, op:go:govulncheck, spell:go

View in Graph Explorer: http://127.0.0.1:7391/console/graph/#view=blast&node=tool%3Ago&token=q2lYk8MY_plBI1553QrP9_LU07Z8kem-6N-iYYUmQME
(start the magus daemon if the graph does not load)
```
<!-- /example -->

A target shows what it runs and what runs it, each relation in plain words:

<!-- example:explain-target-test -->
```console
$ magus explain target:.:test
target:.:test   target
Run the Go test suite; formats first.
source: .
engine: buzz
1 node reaches this

uses        op:go:go-test
depends on  target:.:format
part of     project:.

View in Graph Explorer: http://127.0.0.1:7391/console/graph/#view=blast&node=target%3A.%3Atest&token=q2lYk8MY_plBI1553QrP9_LU07Z8kem-6N-iYYUmQME
(start the magus daemon if the graph does not load)
```
<!-- /example -->

And `path` connects two nodes as a chain - a target reaches its tool through its op:

<!-- example:path-test-to-tool -->
```console
$ magus path target:.:test tool:go
target:.:test -> tool:go  (2 steps)

target:.:test
  uses  op:go:go-test
  uses  tool:go
```
<!-- /example -->

(These blocks are generated by `magus-examples` from a fixture workspace and kept
current by the drift gate; do not hand-edit the output.)

**Where does a function or symbol live (as `path:line`), and where is it used?**

```sh
magus refs <name>                          # the definition + every reference, each as path:line
magus explain "symbol:<id>"                # the node's `source` is the definition's path:line
magus query "kind:symbol <name>" -o json   # each match's `.source` is "path:line"
```

Symbol nodes carry their definition as `source: "path:line"`, and `refs` returns
every reference the same way. An agent (or an MCP tool) can read the exact line
straight from the graph and edit surgically instead of loading the whole file.

**Where does risk concentrate?**

```sh
magus insight hotspots    # churn x complexity per project, with blast radius
magus insight affinity    # projects that change together: hidden coupling
magus insight ownership   # author concentration and bus factor
magus explain <node>      # a node's edges and how many nodes reach it (blast radius)
magus path <a> <b>        # the shortest edge chain between two nodes
```

**Which code lacks test coverage?** magus runs the tests, so it owns the coverage
profile - a pure code-graph tool cannot answer this.

```sh
magus explain "symbol:<id>"      # a function's coverage ratio + test_refs (test files that reference it)
magus query "kind:file" -o json  # each file node's attrs.coverage (covered/total statements)
```

After `magus run coverage` (or `ci`), a `coverage` attr (with `covered_stmts` /
`total_stmts`) folds onto file and symbol nodes, and `test_refs` counts the test files
that reference a symbol. Sort symbols by `coverage` ascending for "what is untested",
or cross it with `insight hotspots` to rank high-churn, low-coverage code first.

**What does a target produce or consume, and is a file generated?** magus indexes each
target's declared `magus.outputs` / `magus.inputs`, so the graph knows the build's file
flow - which a pure code-graph cannot.

```sh
magus explain "target:.:content-generate"   # the files a target produces and consumes
magus explain "doc:docs/spells/go.md"        # a "produced by" edge means it is generated
```

Each declared output/input becomes a `produces` / `consumes` edge to the file and doc
node it matches, so a generated file is self-labeled by its producing target (no marker
needed) and you can walk from a target to exactly what it writes.

**Which markdown is what?** Every authored markdown file in the workspace is a `doc`
node tagged with a `role` from a universal filename convention - so it works in any repo.

```sh
magus query "kind:doc role:agent"    # the agent-instruction files (AGENTS.md, CLAUDE.md)
magus query "role:readme"            # every README, wherever it lives
magus query "kind:doc role:skill"    # skill definitions (SKILL.md)
```

Roles are `readme`, `agent`, `skill`, `changelog`, `contributing`, `license`, or a plain
`doc`. Each doc attaches to the project whose directory holds it (`project --contains-->
doc`), so from a project you reach its README and design notes as contextual docs.

## Graph Explorer

`magus graph open` opens the graph in an interactive, force-directed
[Graph Explorer](console/) in your browser - **privately**. Your graph never
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

### Open your target graph

To open the **target dependency graph** (the `magus.needs` DAG, not the
knowledge graph) in the same explorer:

```sh
magus graph open --targets                # whole workspace
magus graph open --targets .              # scope to root project
magus graph open --targets docs        # scope to one project by path
magus graph open --targets --print        # print the URL instead of opening
```

The `--targets` path always uses the URL fragment (no loopback server); it is
incompatible with `--serve`. An unknown project path exits with code 2 and
lists valid paths.

The explorer's filter box speaks the **same fielded grammar** as `magus query`
(`kind:`, `project:`, `relation:`, `id:`, free text, `"quotes"`, `-negation`); a
query dims non-matching nodes so the subgraph stands out. Beyond the filter:
double-click a node for its **local graph** (its neighborhood, `[`/`]` to change
depth), click a legend color to isolate a kind, and use the **hubs**/**orphans**
lenses (the visual twin of `magus graph stats`). The page is fully client-side and
data-agnostic - it also loads any `graph.json` from `magus graph export -o json`
via the Open-file button or drag-and-drop. This site's own graph is the
[live demo](console/).

## Schema

A node is a magus-domain entity with a stable, human-readable ID
(`<kind>:<qualified-name>`, e.g. `target:pkg/foo:build`). The ID is stable across
builds so external consumers and agent memory can key on it. A rename is a
delete-plus-add.

Node kinds: `project`, `target`, `spell`, `op`, `charm`, `module`, `method`,
`diagnostic`, `doc`, `file`, `function`, `import`, `rationale`, `owner`.

Nodes also carry static metadata the extractors already parse, surfaced as
attributes so `magus explain` answers a question without a second describe: a
project reports its `engine` and `target_count`, each target inherits its
project's `engine`, and a doc page carries its frontmatter `title` and `tags`.
Attributes are additive and absent when unknown, so they never bump the schema
version.

Edges are directed and carry provenance and a confidence tag - `extracted` (1.0,
from a parseable source) or `inferred` (a rubric score, from a fuzzy match).

Relations: `depends_on`, `contains`, `uses`, `calls`, `imports`, `references`,
`documents`, `rationale_for`, `owns`.

Ownership is extracted from a committed `CODEOWNERS` file (checked at the repo
root, `.github/`, or `docs/`): each owner becomes an `owner` node with an `owns`
edge to every project and buzz file it covers, under GitHub's last-match-wins rule,
with `CODEOWNERS:<line>` provenance. Only declared ownership is taken - blame-derived
ownership is insight's job, not a graph edge - so "who owns the blast radius of this
change" is one path query.

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
planning work sees a target's cost without a separate history query. It also folds
each target's `last_output_ref` (the `refxxxxxxxx` id of its most recent captured
run) and `last_run_ok` (`true`/`false` for that run) from the local output store,
so an agent goes query -> target -> the last captured output in two hops
(`magus query output <ref>`). Timings and refs for a
target no longer in any magusfile are dropped rather than left as phantom nodes.
This is the graph's only non-deterministic input, so it is quarantined: a distinct
shard, excluded from remote export, derived from local run records rather than
workspace sources.

## Code symbols (SCIP ingestion)

magus never parses source code. To bring code symbols into the graph, it ingests a
[SCIP](https://docs.sourcegraph.com/code_navigation/explanations/scip) index file
that a per-language indexer (`scip-go`, `scip-typescript`, ...) emits - so any
language with an indexer works, with no magus code per language.

**This is automatic.** Every symbol-capable spell (go, ts, py, rust) exposes a reserved
`scip` op that runs its indexer. Importing the language's spells is the entire opt-in:
each project bound to such a spell is ingested with no `knowledge:` config. Build the
index the same way you run any target:

```sh
magus run pkg/foo::scip   # forks the language's SCIP indexer
```

The index is a build artifact, so it lives under the magus cache dir, never in the
source tree: magus hands the indexer the destination through a `MAGUS_SYMBOL_INDEX`
environment variable it injects for the `scip` op, and reads that same path back at
query time. The next graph query folds the symbols in.

**The daemon keeps it fresh for you.** While the daemon runs, background auto-indexing
re-runs each symbol-capable project's `scip` op when its sources change, so symbols stay
current with no manual step. It is deliberately unobtrusive: a burst of edits coalesces
into one run (a quiet window), a project re-indexes at most once per interval, a run
starts only when nothing else is running, and it cancels itself the moment your own work
needs a slot. Each run goes through the normal path, so it shows up as an ordinary
journaled job, not hidden work. It is on by default in the daemon; a one-shot CLI never
auto-indexes. Tune or disable it under `knowledge.symbol_indexing` (`disabled`,
`quiet_seconds`, `min_interval_seconds`). If an indexer is not installed the background
run just fails and backs off - run `magus run <project>::scip` yourself, or index in CI.

An index that has not been built yet is simply skipped, so symbols appear once the
`scip` target has run. To point a project at an index your own build already emits
somewhere in the tree instead, override it:

```yaml
# magus.yaml
knowledge:
  symbols:
    - project: pkg/foo
      index: build/custom.scip # a workspace-relative path magus reads as-is
```

Each ingested index becomes a per-project `<project>@symbols` shard: `symbol` nodes
(keyed by their version-stripped SCIP moniker), `defines` edges from the defining
file, and `references` edges from each using file (one per file, carrying an
occurrence count and capped lines). A symbol seen only as a reference still gets a
node, so cross-project usage resolves. Every indexed source file also becomes a
browsable `file` node the edges land on, linked to the project that owns it - so a
`.go` or `.ts` file sits in the graph the same way a `.buzz` file does, reachable from
its project and the workspace. SCIP paths are relative to the indexer's root, so magus
rebases them onto the project's workspace path; a nested project's files land under the
right project, not the workspace root.

Both file and symbol nodes carry a `language` attr, so `magus query language:go`
groups every Go source file and symbol - and `language:buzz` the buzz sources - one
filter across everything the graph knows, however it was extracted (magus's own AST
walk or a foreign SCIP index).

Symbol shards can dwarf the domain graph, so they are **lazily loaded**: the default
query/stats/`graph open`/warm graph never touch them. They load only when a query is
symbol-seeded - `kind:symbol`, a `symbol:` ID, `relation:defines`/`references`, or
the `refs` verb. `magus refs <symbol>` lists a symbol's definition and every
referencing file (`magus_refs` over MCP, paginated). At very large scale a derived
`shards/@symbols.routing.json` (symbol hash to referencing shard names, rebuilt with
the shards) lets an exact-ID lookup load only the shards that mention the symbol
rather than all of them; a missing routing file just falls back to loading all.

## Git history (@vcs)

Opt-in, off by default: enable it to fold each file's git history onto its file node.

```yaml
# magus.yaml
knowledge:
  vcs:
    enabled: true
    max_commits: 1000 # optional: bound the history walk (default 1000)
    authorship: true # optional: include author nodes + authored edges (default on)
```

When enabled and the workspace is a git repo, a `@vcs` shard adds four attrs to every
file node: `vcs_last_commit` (short SHA of the most recent commit touching the file),
`vcs_last_modified` (its date), `vcs_last_author` (who last touched it), and
`vcs_commits` (commits touching the file within the window). It also mints an `author`
node per contributor with an `authored` edge to each file they touched in the window -
who edits what, so an agent can ask `explain author:Ada` or trace ownership. These edges
are uncapped: `max_commits` already bounds the scan, so a dominant maintainer legitimately
having many is a fact to teach, not a smell. Set `authorship: false` to keep only the
per-file `vcs_*` attrs and drop the author layer.

The values are EXTRACTED from git and deterministic per commit, so the shard is
remote-shareable like the other extracted shards. The `git log` walk is bounded by
`max_commits` and keyed by an input fingerprint (schema, HEAD, the window, the dirty-file
set, and the `authorship` flag), so the standard shard store reuses it whole and it
re-runs only when one of those actually moves - never on the query path. A non-git
workspace or a git error simply yields no shard. Because the `vcs_*` attrs vary by commit,
`graph diff` strips them from both sides, so a file node is not reported as changed just
because its last commit moved - the diff stays structural.

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

## Diffing against a baseline

`magus graph diff` reports what a branch did to the domain's shape: the nodes and
edges added or removed, and (for nodes) which fields changed. Export a baseline on
the base branch, then diff the working tree against it - the PR blast-radius artifact.

```sh
magus graph diff --rev HEAD~1                    # against a git revision, no export needed
magus graph diff --rev main -o markdown          # a CI comment vs the base branch

git stash && magus graph export -o json > /tmp/base.json && git stash pop
magus graph diff /tmp/base.json                  # against an export file
magus graph diff /tmp/base.json -o json          # machine-readable, with before/after
```

`--rev` builds the base graph from that revision's tracked files (domain-only, using
the current config) in an isolated throwaway tree that never touches your real cache;
it and the positional baseline are mutually exclusive, and it cannot be combined with
`--global` (the base is a single-workspace build). A baseline file must be a whole-graph
`graph export -o json` (symbol shards in it are matched automatically; pass `--global`
if the baseline was global). Edge diffs are structural -
an edge is identified by (source, target, relation), so a re-scored or re-provenanced
edge that keeps those three is not reported as a change.

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
`magus_path`, `magus_stats`, and `magus_refs` (plus `magus_output`, which retrieves a
target's captured output by ref). See [MCP](mcp.md) for wiring. Prefer
these over grep to find and relate magus-domain entities; start from the `MAGUS.md`
routing table, which is already in context in a fresh clone.

For a large result set, `magus_query` and `magus_refs` page: pass `limit` to cap the
rows per response and echo the returned `next_cursor` to fetch the next page. The
cursor is stateless and self-validating - it carries the query and a graph
fingerprint, so a cursor reused against a different query or a graph that changed
between pages is rejected rather than returning an incoherent slice.

`magus agent install claude` writes four skills into `.claude/skills/` that teach
an agent HOW to use magus (the repo's `MAGUS.md` says WHAT is in the workspace):
the knowledge-graph verbs, target-first execution, generated-file triage, and
graph-grounded refactoring. The skills ship with the binary and teach only the
tool surface, so they stay current with the magus version rather than the
workspace. Platform is an explicit argument; only `claude` is supported today.
Each installed file carries a version footer, and `magus graph verify` (with
`--strict` for CI) reports when an installed skill has fallen behind the binary
after an upgrade. See [Agents](agents.md) for the full surface.

## Prior art

Two projects shaped this design, and both deserve credit. The thread starts
earlier than either: Andrej Karpathy's April 2026 post on X described keeping
a raw folder of papers, notes, and screenshots and wanting to query across it
without rereading every file. Graphify was built within days as a direct
answer to that post, and the magus knowledge graph is a further step down the
same path: the querying idea applied to the one domain a build tool already
understands precisely.

[Graphify](https://github.com/Graphify-Labs/graphify) established the pattern
of a queryable, committable code knowledge graph with an honest audit trail,
and its verb vocabulary was good enough that magus reuses it outright: query,
explain, path. The two tools have different jobs, though. Graphify is a
general corpus indexer - point it at any folder of code, docs, papers, or
media and it extracts a graph, using an LLM pass for non-code content. magus
only ever models its own domain, and its graph is assembled entirely from
declarations it already verifies as a build tool (the project DAG, target
sources and outputs, spell and module registries), so the build is
deterministic, runs with zero LLM involvement, and stays cache-owned rather
than committed. If you want a graph of an arbitrary corpus, Graphify is the
right tool; the magus graph is narrower and, within its domain, checkable
edge by edge.

[Obsidian](https://obsidian.md) shaped the memory side, and its influence
predates the AI wave entirely: durable knowledge as plain, linked markdown
files the user owns, readable by any tool, has been its position for years. magus
memory (status, progress, decisions) borrows that files-first stance
deliberately - the files work without magus. Obsidian is a knowledge base for
people; magus keeps three small files per repository for agents and the
humans working alongside them, and stops there.
