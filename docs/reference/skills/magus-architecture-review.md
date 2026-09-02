---
title: magus-architecture-review
generated_from: internal/agent/skills/magus-architecture-review/SKILL.md
description: "Ground refactoring and structure proposals in the magus knowledge graph instead of intuition."
tags: [agents, skills, magus-architecture-review]
aliases:
  - reference/skills/magus-architecture
skill_full_bytes: 6322
skill_simple_bytes: 5123
---

# magus-architecture-review

Ground refactoring and structure proposals in the magus knowledge graph instead of intuition. Use when suggesting directory structure, package layout, or module boundaries, when deciding where new code belongs, when assessing the blast radius or risk of a refactor, or when asked where a magus workspace's coupling and churn concentrate.

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
| `agent-skill-version` | `50` |
| `knowledge-schema-version` | `10` |
| `skill-content` | `e4b75fa969de` |
| `skill-variant` | `full` |

The `skill-content` digest covers this skill alone, and both permutations below report it: they go stale together, never one silently, and a change to another skill does not move it.

## Full form

Every mechanical step spelled out, plus the rationale for each. Installed as the `<name>-full` twin: loaded by name rather than always, so a reader who needs the long form can ask for it without every session carrying it.

````markdown
# Architecture decisions from the graph

magus already measured the workspace: what depends on what, what changes
together, where churn and complexity concentrate, who owns what. Query those
facts before proposing structure; a proposal that cites graph evidence is
checkable, one from intuition is vibes.

## Survey before proposing

Run these and read them together:

```sh
magus graph stats            # god nodes (structural risk), orphans, doc coverage
magus_insight lens=hotspots  # churn x complexity per project, with blast radius
magus_insight lens=affinity  # projects that change together: hidden coupling
magus_insight lens=ownership # author concentration, bus factor, abandonment
magus graph deps -o tree     # the declared project DAG
```

MCP: `magus_stats`, `magus_insight` {lens}, and `magus_query` cover the same
ground. Affinity deserves special weight: two projects that keep changing
together WITHOUT a declared dependency edge are coupled through the back door -
either declare the dependency or move the shared concern.

## Then survey the opposite: what is too THIN to justify a boundary

Every lens above finds something too big, too central, or too churned. None find
the inverse, and over-abstraction is the more common failure in a young codebase.
Ask it explicitly, because nothing prompts it for you:

A boundary is not free. In Go every package boundary FORCES an export:
a helper that would be lowercase inside one package must be capitalized to cross
into another. So splitting files into packages to "organize" them WIDENS the
public surface you were trying to keep small, and each new export is a name you
must justify, document, and keep stable. The cost is paid per boundary, and no
churn or coupling metric records it.

The shapes worth flagging, in rough order of how clearly they are wrong:

| Shape | Why it is suspect |
|---|---|
| Imported only from inside its own subtree | It is a parent's implementation, not a boundary |
| Exactly one importer, and no encapsulation behind it | A file in the wrong place |
| Single file, single exported symbol | The package name is a second name for one function |

Note the third column that is NOT there: size. Small is not the same as needless.
Check what a package HIDES before proposing a merge - one exported function over
four unexported helpers is real encapsulation at any line count, and two importers
in different trees means merging makes one depend on the other.

`magus graph stats` reports orphans (zero importers), which is adjacent but not
the same question: the expensive cases have one importer, not none.

WRONG: proposing a merge because a package is under N lines.
CORRECT: proposing a merge because its importers all live inside its own parent,
and nothing it exports would need to be exported once merged.

## Sizing a specific refactor

1. Blast radius of a node: `magus explain <node>` shows its edges and how many
   nodes reach it. A high reached-by count means migration plan, not quick
   rename.
2. Fan-in of a symbol: `magus refs <symbol>` lists the defining file and every
   referencing file:line from the SCIP index. Run it before moving or renaming
   any exported symbol. An empty result states which kind of empty it is:
   `absent` is verified, `unknown` names the projects with no symbol index - build
   them with `magus graph build` before trusting it.
3. How two things relate: `magus path <a> <b>` gives the shortest edge chain -
   use it to test whether a proposed boundary actually separates them.
4. Owners: `magus query kind=owner` (populated from CODEOWNERS) tells you whose
   review a move needs.

## Match the existing conventions

Derive the pattern from the graph rather than imposing one: where similar code
already lives (`magus query kind=<kind> <term>`), which modules import which
(`relation=imports`), how existing projects segment (`magus graph deps`). A
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
  printf "%-11s %s\n" "$k" "$(magus query "kind=$k" -o json | jq length)"
done                                  # population per abstraction
magus explain "<node>"               # compare a kind's edges against a neighbor's
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
````

## Short form

The enumeration dropped, the judgment kept - for the most capable readers, not the least; the bar under the heading above shows by how much. This is the always-loaded primary. Both are hand-authored from one source body; see [Skills](../../guides/integrations/agents/skills.md) for the difference.

<details>
<summary>Show the short form</summary>

````markdown
# Architecture decisions from the graph

magus already measured the workspace: what depends on what, what changes
together, where churn and complexity concentrate, who owns what. Query those
facts before proposing structure; a proposal citing graph evidence is
checkable, one from intuition is not.

## Survey before proposing

Run these and read them together:

```sh
magus graph stats            # god nodes (structural risk), orphans, doc coverage
magus_insight lens=hotspots  # churn x complexity per project, with blast radius
magus_insight lens=affinity  # projects that change together: hidden coupling
magus_insight lens=ownership # author concentration, bus factor, abandonment
magus graph deps -o tree     # the declared project DAG
```

MCP: `magus_stats`, `magus_insight` {lens}, and `magus_query` cover the same
ground. Weight affinity most: changing
together with no declared edge is back-door coupling.

## Then survey the opposite: what is too THIN to justify a boundary

Every lens above finds something too big, too central, or too churned. None find
the inverse, and over-abstraction is the more common failure in a young codebase.
Ask it explicitly, because nothing prompts it for you:

A boundary is not free: in
Go, splitting a package forces exports, widening the surface you meant to shrink.
No churn or coupling metric records that cost.

The shapes worth flagging, in rough order of how clearly they are wrong:

| Shape | Why it is suspect |
|---|---|
| Imported only from inside its own subtree | It is a parent's implementation, not a boundary |
| Exactly one importer, and no encapsulation behind it | A file in the wrong place |
| Single file, single exported symbol | The package name is a second name for one function |

Note the third column that is NOT there: size. Small is not the same as needless.
Check what a package HIDES before proposing a merge - one exported function over
four unexported helpers is real encapsulation at any line count, and two importers
in different trees means merging makes one depend on the other.

`magus graph stats` reports orphans (zero importers), which is adjacent but not
the same question: the expensive cases have one importer, not none.

WRONG: proposing a merge because a package is under N lines.
CORRECT: proposing a merge because its importers all live inside its own parent,
and nothing it exports would need to be exported once merged.

## Sizing a specific refactor

1. Blast radius of a node: `magus explain <node>` shows its edges and how many
   nodes reach it.
2. Fan-in of a symbol: `magus refs <symbol>` lists the defining file and every
   referencing file:line from the SCIP index. Run it before moving or renaming
   any exported symbol. An empty result carries a
   verdict; `unknown` means an index is missing, not that nothing uses it.
3. How two things relate: `magus path <a> <b>` gives the shortest edge chain.
4. Owners: `magus query kind=owner` (populated from CODEOWNERS) tells you whose
   review a move needs.

## Match the existing conventions

Derive the pattern from the graph rather than imposing one: where similar code
already lives (`magus query kind=<kind> <term>`), which modules import which
(`relation=imports`), how existing projects segment (`magus graph deps`). State the observed convention in the proposal, with the query
that shows it.

## Audit the domain model itself

Census the kinds, then read the stats for smells:

```sh
magus graph stats                    # god nodes, orphans, doc coverage
for k in project target spell op tool charm module method diagnostic doc file \
         function symbol import owner; do
  printf "%-11s %s\n" "$k" "$(magus query "kind=$k" -o json | jq length)"
done                                  # population per abstraction
magus explain "<node>"               # compare a kind's edges against a neighbor's
```

Confirm each smell against the source before acting on it:

- A SINGLETON kind (one member) is often over-modeled - does it earn a distinct
  kind, or fold into an attr on an existing one?
- Two kinds with near-identical population AND edge shape may be one concept
  under two names. Keep them distinct only if their PROVENANCE differs (the kind
  doctrine in `types/knowledge.go`).
- An ORPHAN (nothing links to it) is dead weight or a missing edge - decide
  which.
- A NODE LABEL that varies by checkout (a worktree name where a stable module
  name belongs) is an identity smell.

A kind or edge earns its place only if it answers a question the others cannot;
prefer folding into an existing mechanism over adding one. Ground every claim in a query, exactly as for a layout proposal.

## Verify the change

After restructuring, show the impact in graph terms: `magus graph diff --rev
<base> -o markdown` lists the nodes and edges the change added, removed, or
altered. Then run
`magus affected ci` to prove the affected projects still pass.

## Do not render the graph yourself

magus emits; it does not render. To look at structure, offer an export
(`magus graph export -o json` or `-o graphml`) that opens in Gephi, yEd, or a
browser graph tool.
````


</details>
