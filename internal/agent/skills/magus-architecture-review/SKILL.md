# Architecture decisions from the graph

magus already measured the workspace: what depends on what, what changes together,
where churn and complexity concentrate, who owns what. Query those facts before
proposing structure{{if .Full}}; a proposal that cites graph evidence is
checkable, one from intuition is vibes{{else}}; a proposal citing graph evidence is
checkable, one from intuition is not{{end}}.

## Survey before proposing

Run these and read them together:

```sh
magus graph stats            # god nodes (structural risk), orphans, doc coverage
magus graph deps -o tree     # the declared project DAG
```

MCP: `{{tool "client"}}` covers the same ground through `{{buzz "stats"}}`, `{{buzz "insight"}}`
(hotspots, affinity, and ownership on one report), and `{{buzz "query"}}`.
Insight has no CLI verb; without MCP, read one lens through `magus buzz`:

```sh
magus buzz -e 'import "std"; import "encoding/json"; import "magus"; fun main(args: [str]) > void !> str { std\print(json\stringify(magus\insight().affinity)); }'
```

{{if .Full}} Affinity deserves special weight: two projects that keep changing
together WITHOUT a declared dependency edge are coupled through the back door:
either declare the dependency or move the shared concern.{{else}} Weight affinity most: changing
together with no declared edge is back-door coupling.{{end}}

## Then survey the opposite: what is too thin to justify a boundary

Every lens above finds something too big, too central, or too churned. None finds
the inverse, and over-abstraction is the more common failure in a young codebase.
Ask it explicitly; nothing prompts it.

{{if .Full}}A boundary is not free. In Go every package boundary FORCES an export:
a helper that would be lowercase inside one package must be capitalized to cross
into another. So splitting files into packages to "organize" them WIDENS the
public surface you were trying to keep small, and each new export is a name you
must justify, document, and keep stable. The cost is paid per boundary, and no
churn or coupling metric records it.{{else}}A boundary is not free: in
Go, splitting a package forces exports, widening the surface you meant to shrink.
No churn or coupling metric records that cost.{{end}}

The shapes worth flagging, most clearly wrong first:

| Shape | Why it is suspect |
|---|---|
| Imported only from inside its own subtree | It is a parent's implementation, not a boundary |
| Exactly one importer, and no encapsulation behind it | A file in the wrong place |
| Single file, single exported symbol | The package name is a second name for one function |

Size is NOT a column: small is not needless. Check what a package HIDES before
proposing a merge:

- One exported function over four unexported helpers is real encapsulation at any
  line count.
- Two importers in different trees means a merge makes one depend on the other.

`magus graph stats` reports orphans (zero importers), an adjacent question. The
expensive cases have one importer, not none.

WRONG: proposing a merge because a package is under N lines.
CORRECT: proposing a merge because its importers all live inside its own parent,
and nothing it exports would need to be exported once merged.

## Sizing a specific refactor

1. Blast radius of a node: `magus explain <node>` shows its edges and how many
   nodes reach it.{{if .Full}} A high reached-by count means migration plan, not quick
   rename.{{end}}
2. Fan-in of a symbol: `magus refs <symbol>` lists the defining file and every
   referencing file:line from the SCIP index. Run it before moving or renaming any
   exported symbol.{{if .Full}} An empty result states which kind of empty it is:
   `absent` is verified, `unknown` names the projects with no symbol index; build
   them with `magus graph build` before trusting it.{{else}} An empty result carries a
   verdict; `unknown` means an index is missing, not that nothing uses it.{{end}}
3. How two things relate: `magus path <a> <b>` gives the shortest edge chain{{if .Full}};
   use it to test whether a proposed boundary separates them{{end}}.
4. Owners: `magus query kind=owner` (from CODEOWNERS) says whose review a move
   needs.

## Match the existing conventions

Derive the pattern from the graph instead of imposing one:

- where similar code lives: `magus query kind=<kind> <term>`;
- which modules import which: `relation=imports`;
- how projects segment: `magus graph deps`.{{if .Full}}

A suggestion that follows the workspace's own conventions costs less than an
imported ideal.{{end}}

State the observed convention in the proposal, with the query that shows it.

## Audit the domain model itself

{{if .Full}}The graph is also a lens on its OWN abstractions: use it to scrutinize kinds,
names, and boundaries, not just code layout. Census the kinds, then read the
stats for smells (see the {{skill "query"}} skill for the query syntax):{{else}}Census the kinds, then read the stats for smells:{{end}}

```sh
magus graph stats                    # god nodes, orphans, doc coverage
{{- if .Full}}
for k in project target spell op tool charm module method diagnostic doc file \
         function symbol import owner; do
  printf "%-11s %s\n" "$k" "$(magus query "kind=$k" -o json | jq length)"
done                                  # population per abstraction
{{- else}}
magus query "kind=<kind>" -o json    # population of one abstraction
{{- end}}
magus explain "<node>"               # compare a kind's edges against a neighbor's
```

Confirm each smell against the source before acting on it:

- A SINGLETON kind (one member) is often over-modeled. Does it earn a distinct
  kind, or fold into an attr on an existing one?
- Two kinds with near-identical population AND edge shape may be one concept under
  two names. Keep them distinct only if their PROVENANCE differs (the kind
  doctrine in `types/knowledge.go`){{if .Full}}: a kind whose every instance is derivable
  from another kind's attr fails that test and should fold{{end}}.
- An ORPHAN (nothing links to it) is dead weight or a missing edge; decide
  which{{if .Full}}; an undeclared-but-available builtin is neither{{end}}.
- A NODE LABEL that varies by checkout (a worktree name where a stable module name
  belongs) is an identity smell{{if .Full}}, even when the ID is stable{{end}}.

A kind or edge earns its place only by answering a question the others cannot.
Prefer folding into an existing mechanism over adding one{{if .Full}} (pre-1.0: break
freely){{end}}. Ground every claim in a query, as for a layout proposal.

## Say when not to build it

A mechanism that ACTS (a guard, a refusal, a cancellation, an auto-fix) is judged on
its wrong firings. Name the two cases its predicate cannot separate and the cost of
guessing each wrong. When it cannot separate them, do not build it{{if .Full}}, and say so rather
than shipping a check that fires on the wrong one{{end}}.

Wrong firings are the expensive direction: a gap gets noticed, while a check that
cries wolf teaches people to route around it{{if .Full}}, taking the real findings with it{{end}}.

## Verify the change

After restructuring, show the impact in graph terms: `magus graph diff --rev <base>
-o markdown` lists the nodes and edges the change added, removed, or
altered{{if .Full}} (blast radius as data, suitable for a PR description){{end}}. Then run
`magus affected ci` to prove the affected projects still pass.

## Do not render the graph yourself

magus emits; it does not render. To look at structure, offer an export
(`magus graph export -o json` or `-o graphml`) for Gephi, yEd, or a browser graph
tool{{if .Full}}; do not hand-draw diagrams of what the graph already knows{{end}}.
