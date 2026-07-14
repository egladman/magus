---
name: magus-architecture
description: Ground refactoring and structure proposals in the magus knowledge graph instead of intuition. Use when suggesting directory structure, package layout, or module boundaries, when deciding where new code belongs, when assessing the blast radius or risk of a refactor, or when asked where a magus workspace's coupling and churn concentrate.
---

# Architecture decisions from the graph

magus already measured the workspace: what depends on what, what changes
together, where churn and complexity concentrate, who owns what. Query those
facts before proposing structure; a proposal that cites graph evidence is
checkable, one from intuition is vibes.

## Survey before proposing

Run these and read them together:

```sh
magus graph stats            # god nodes (structural risk), orphans, doc coverage
magus insight hotspots       # churn x complexity per project, with blast radius
magus insight affinity       # projects that change together: hidden coupling
magus insight ownership      # author concentration, bus factor, abandonment
magus graph deps -o tree     # the declared project DAG
```

MCP: `magus_stats`, `magus_insight` {lens}, and `magus_query` cover the same
ground. Affinity deserves special weight: two projects that keep changing
together WITHOUT a declared dependency edge are coupled through the back door -
either declare the dependency or move the shared concern.

## Sizing a specific refactor

1. Blast radius of a node: `magus explain <node>` shows its edges and how many
   nodes reach it. A high reached-by count means migration plan, not quick
   rename.
2. Fan-in of a symbol: `magus refs <symbol>` lists the defining file and every
   referencing file:line from the SCIP index. Run it before moving or renaming
   any exported symbol. (If it reports no match for a symbol that surely
   exists, the index is likely unbuilt: check `magus status`, then
   `magus graph build`.)
3. How two things relate: `magus path <a> <b>` gives the shortest edge chain -
   use it to test whether a proposed boundary actually separates them.
4. Owners: `magus query kind:owner` (populated from CODEOWNERS) tells you whose
   review a move needs.

## Match the existing conventions

Derive the pattern from the graph rather than imposing one: where similar code
already lives (`magus query kind:<kind> <term>`), which modules import which
(`relation:imports`), how existing projects segment (`magus graph deps`). A
suggestion that follows the workspace's own conventions costs less than an
imported ideal. State the observed convention in the proposal, with the query
that shows it.

## Verify the change

After restructuring, show the impact in graph terms: `magus graph diff --rev
<base> -o markdown` lists the nodes and edges the change added, removed, or
altered - blast radius as data, suitable for a PR description. Then run
`magus affected ci` to prove the affected projects still pass.

## Do not render the graph yourself

magus emits; it does not render. To look at structure, offer an export
(`magus graph export -o json` or `-o graphml`) that opens in Gephi, yEd, or a
browser graph tool - do not hand-draw diagrams of what the graph already knows.
