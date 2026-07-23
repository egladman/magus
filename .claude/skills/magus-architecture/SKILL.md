---
name: magus-architecture
description: Ground refactoring and structure proposals in the magus knowledge graph instead of intuition. Use when suggesting directory structure, package layout, or module boundaries, when deciding where new code belongs, when assessing the blast radius or risk of a refactor, or when asked where a magus workspace's coupling and churn concentrate.
license: GPL-3.0-or-later
compatibility: any-agent
metadata:
  source: magus
  agent-skill-version: 12
  knowledge-schema-version: 6
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

## Audit the domain model itself

The graph is also a lens on its OWN abstractions - use it to scrutinize kinds,
names, and boundaries, not just code layout. Census the kinds, then read the
stats for smells (see the magus-query skill for the query syntax):

```sh
magus graph stats                    # god nodes, orphans, doc coverage
for k in project target spell op tool charm module method diagnostic doc file \
         function symbol import owner; do
  printf "%-11s %s\n" "$k" "$(magus query "kind:$k" -o name | grep -c .)"
done                                  # population per abstraction
magus explain "<node>"               # compare a kind's edges against a neighbour's
```

Confirm each smell against the source before acting on it:

- A SINGLETON kind (one member) is often over-modeled - does it earn a distinct
  kind, or fold into an attr on an existing one?
- Two kinds with near-identical population AND edge shape may be one concept
  under two names. Keep them distinct only if their PROVENANCE differs (the kind
  doctrine in `types/knowledge.go`): a kind whose every instance is derivable
  from another kind's attr fails that test and should fold.
- An ORPHAN (nothing links to it) is dead weight or a missing edge - decide
  which; an undeclared-but-available builtin is neither.
- A NODE LABEL that varies by checkout (a worktree name where a stable module
  name belongs) is an identity smell, even when the ID is stable.

A kind or edge earns its place only if it answers a question the others cannot;
prefer folding into an existing mechanism over adding one (pre-1.0: break
freely). Ground every claim in a query, exactly as for a layout proposal.

## Verify the change

After restructuring, show the impact in graph terms: `magus graph diff --rev
<base> -o markdown` lists the nodes and edges the change added, removed, or
altered - blast radius as data, suitable for a PR description. Then run
`magus affected ci` to prove the affected projects still pass.

## Do not render the graph yourself

magus emits; it does not render. To look at structure, offer an export
(`magus graph export -o json` or `-o graphml`) that opens in Gephi, yEd, or a
browser graph tool - do not hand-draw diagrams of what the graph already knows.

<!-- generated by: magus agent install; agent-skill-version: 12; knowledge-schema-version: 6; do not edit, re-run to update -->
