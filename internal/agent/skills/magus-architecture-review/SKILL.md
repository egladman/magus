# Architecture decisions from the graph

magus already holds the workspace's structure: which package imports which, the
directory tree, declared layers, each symbol's callers, churn, and ownership. Gather
architecture evidence from it, not by reading files{{if .Full}}. A structure claim
built from Read and Grep is a sample of what you happened to open; the graph holds
every package{{end}}.

## Fast path: one command per question

| question | run | read |
| --- | --- | --- |
| what does X import; what imports X | `{{cmd "explain"}} dir:<path>` | the `imports` and `imported by` edges |
| what transitively depends on X | `{{buzz "neighborhood"}}` with `direction = "in"` (below) | `nodes`; `"out"` walks what X needs |
| is there an import cycle; would A importing B make one | the `{{buzz "importGraph"}}` script below | `cycle edge` lines, or `B already reaches A: true` |
| is this layer clean | `{{buzz "layer"}}("<name>")` | each covered Dir's `imports` against the layers it may use |
| what already does this; where does new code belong | `{{cmd "query"}} "kind=symbol <name>"` | existing implementations and their files |
| who calls this symbol | `{{cmd "explain"}} '<symbol id>'` | `called by` symbols and `referenced by` files |
| what a file holds | `{{cmd "explain"}} <path/to/file>` | `defines (N)` |
| which packages are central | `{{cmd "graph stats"}} --symbols --kind dir` | the `IN` column: fan-in |
| how churned, owned or duplicated | `{{buzz "insight"}}()` | `hotspots`, `ownership`, `duplication` |
| what breaks if I change a symbol | `{{cmd "refs"}} <symbol> --occurrences` | `N occurrence(s) in M file(s)` |
| what a changeset reaches | `{{cmd "affected"}} --impact` | the seed and affected projects |

A symbol id is the `symbol:...` id a `kind=symbol` query prints; quote it, since it
holds spaces and backticks.

Run a Buzz call through `{{cmd "buzz"}}` (`-e` for a one-liner, or a script file) or
the `{{tool "client"}}` MCP tool. Load {{skill "buzz-lang"}} before writing one. Save
the transitive walk as `dependents.buzz` and run `{{cmd "buzz"}} dependents.buzz <path>`:

```buzz
import "std";
import "magus";

fun main(args: [str]) > void !> any {
    final nb = magus\neighborhood(args[0], opts: magus\NeighborhoodOptions{ depth = 3, relations = ["imports"], direction = "in" });
    std\print("verdict {nb.answer.verdict}");
    foreach (n in nb.nodes) {
        std\print(n.id);
    }
}
```

`{{buzz "dir"}}("<path>")` is the same card as a record: `imports`, `importedBy`,
`files`, `layer`, and `importsIndexed`. `{{buzz "dirs"}}("<glob>", opts:
magus\DirsOptions{ layer = "<name>" })` filters directories by layer, `language` or
`depth`.

## Every structural claim carries its evidence

A proposal states facts about structure: what imports what, what is central, what is
duplicated, what a move breaks. Attach to EACH fact the command that produced it and
its output ref or a pasted excerpt. Drop any claim without one.

WRONG: "pkg/store is a god package and leans on pkg/config."
CORRECT: "pkg/store has 31 importers (`{{cmd "explain"}} dir:pkg/store`, 31 `imported
by` edges) and imports pkg/config (the same card's `imports`)."

Read source only to confirm a specific line the graph already pointed at. Never read
files to discover structure{{if .Full}}: a directory listing, a grep for `import`,
or opening each package in turn finds what you opened and misses the rest{{end}}.

An empty or thin answer is evidence only once its verdict says so:

- `importsIndexed: false` on a Dir means no symbol index read that directory. Run
  `{{cmd "graph build"}}`, then ask again.
- A verdict of `unknown` means some project had no symbol index; the CLI names
  which. Build it, or cite the answer as partial and name the gap.
- A `stale index` note means the tree moved since the last index. The answer may
  miss new sites; rebuild before you cite it.
- `absent` is verified: magus searched every index the workspace declares.

## Cycles and layering

There is no built-in cycle check. `{{buzz "importGraph"}}()` maps every package to
the packages it imports, which is enough. Save this as `cycles.buzz`. Run
`{{cmd "buzz"}} cycles.buzz` to list every import edge that sits on a cycle. Run
`{{cmd "buzz"}} cycles.buzz <A> <B>` to ask whether A importing B would close one.

```buzz
import "std";
import "magus";

fun reaches(pkgs: {str: [str]}, start: str, target: str) > bool {
    final seen = mut {start: true};
    final todo = mut [start];
    while (todo.len() > 0) {
        foreach (dep in pkgs[todo.pop()!] ?? []) {
            if (dep == target) {
                return true;
            }
            if (!(seen[dep] ?? false)) {
                seen[dep] = true;
                todo.append(dep);
            }
        }
    }
    return false;
}

fun main(args: [str]) > void !> any {
    final g = magus\importGraph();
    if (!g.indexed) {
        throw "no symbol index: build one with magus graph build";
    }
    if (args.len() == 2) {
        std\print("{args[1]} already reaches {args[0]}: {reaches(g.packages, start: args[1], target: args[0])}");
        return;
    }
    foreach (pkg, deps in g.packages) {
        foreach (dep in deps) {
            if (reaches(g.packages, start: dep, target: pkg)) {
                std\print("cycle edge: {pkg} -> {dep}");
            }
        }
    }
}
```

A move adds import edges. Every package that calls the moved code now imports its
new home, and the new home imports whatever the code calls. Ask the two-argument
form once per new edge{{if .Full}}. `refs --occurrences` lists the calling files,
and so the packages a move touches{{end}}.

Layering is declared, never inferred. `{{buzz "layer"}}("<name>")` returns the Dirs a
declared layer covers, and raises naming the declared layers when the name is not
one. With no layers declared, say so; do not invent them from directory names.

## What only looks like an answer

Each of these has a correct command in the fast path.

- `{{cmd "graph stats"}}` without `--symbols` counts no package imports, so its god
  nodes are not packages. Its orphans are unused spells and isolated nodes, not
  unimported packages.
- `--kind dir` degree counts contained files too, so a directory of many files
  ranks high on `OUT` alone. Read `IN` for fan-in.
- `{{cmd "path"}}` walks edges in both directions. A found path says two nodes
  connect, not that one depends on the other. Use `{{buzz "neighborhood"}}` with a
  `direction`.
- The `N nodes reach this` line on an `{{cmd "explain"}}` card saturates: a private
  helper with one caller reports over a thousand. It does not size a migration;
  `refs --occurrences` does.
- `affinity` in `{{buzz "insight"}}` pairs PROJECTS, so inside one project it sees
  nothing.
- `hotspots` ranks generated files too. Run `{{cmd "describe file"}} <path>` before
  calling a hotspot a design problem.
- `kind=owner` exists only where the repo commits a CODEOWNERS file. Otherwise use
  the `ownership` lens: each project's primary author and `busFactor1`.
- Free-text `{{cmd "query"}}` does not search symbols; name `kind=symbol`.
  `relation=imports` matches nothing without `kind=dir`.
- `{{cmd "graph deps"}}` is the declared project DAG, not package imports.

## Then survey the opposite: what is too thin to justify a boundary

Every lens above finds something too big, too central, or too churned. None finds
the inverse, and over-abstraction is the more common failure in a young codebase.
Ask it explicitly; nothing prompts it.

{{if .Full}}A boundary is not free. In Go every package boundary FORCES an export:
a helper that would be lowercase inside one package must be capitalized to cross
into another. So splitting files into packages to "organize" them WIDENS the
public API you were trying to keep small, and each new export is a name you
must justify, document, and keep stable. The cost is paid per boundary, and no
churn or coupling metric records it.{{else}}A boundary is not free: in
Go, splitting a package forces exports, widening the public API you meant to shrink.
No churn or coupling metric records that cost.{{end}}

The shapes worth flagging, most clearly wrong first:

| shape | why it is suspect | shown by |
| --- | --- | --- |
| imported only from inside its own subtree | it is a parent's implementation, not a boundary | every `importedBy` entry of `{{buzz "dir"}}("<path>")` sits under its parent |
| exactly one importer, and no encapsulation behind it | a file in the wrong place | one `importedBy` entry; `{{cmd "explain"}} <file>` shows what it defines |
| single file, single exported symbol | the package name is a second name for one function | `files` is 1 on the Dir; `{{cmd "explain"}} <file>` |

Size is NOT a column: small is not needless. Check what a package HIDES before
proposing a merge:

- One exported function over four unexported helpers is real encapsulation at any
  line count.
- Two importers in different trees means a merge makes one depend on the other.

WRONG: proposing a merge because a package is under N lines.
CORRECT: proposing a merge because its importers all live inside its own parent,
and nothing it exports would need to be exported once merged.

## Fold before adding

Before proposing new code, a new package, or a new abstraction, show that nothing
already does the job:

1. `{{cmd "query"}} "kind=symbol <name>"` for each name the new code would need. Try
   synonyms; a second implementation under another name is the common case.
2. Read the `duplication` lens of `{{buzz "insight"}}()`: functions that call the same
   symbols in the same proportions, which is what copied logic looks like.
3. Place new code where the `imports` of `{{buzz "dir"}}` already cover what it
   needs, so the change adds no import edge.

{{if .Full}}A suggestion that follows the workspace's own conventions costs less than
an imported ideal. {{end}}State the observed convention in the proposal, with the
command that shows it.

## Audit the domain model itself

{{if .Full}}The graph is also a lens on its OWN abstractions: use it to scrutinize kinds,
names, and boundaries, not just code layout. Census the kinds, then read the
stats for smells (see the {{skill "query"}} skill for the query syntax):{{else}}Census the kinds, then read the stats for smells:{{end}}

```sh
magus query "kind=<kind>"          # the matches line is the population
magus graph stats --kind <kind>    # god nodes and orphans of that kind
magus explain "<node>"             # compare a kind's edges against a neighbor's
```

Then ask of each finding:

- A SINGLETON kind (one member) is often over-modeled. Does it earn a distinct
  kind, or fold into an attr on an existing one?
- Two kinds with near-identical population AND edge shape may be one concept under
  two names. Keep them distinct only if their PROVENANCE differs{{if .Full}}: a kind
  whose every instance is derivable from another kind's attr fails that test and
  should fold{{end}}.
- An ORPHAN (nothing links to it) is dead weight or a missing edge; decide
  which{{if .Full}}; an undeclared-but-available builtin is neither{{end}}.
- A NODE LABEL that varies by checkout (a worktree name where a stable module name
  belongs) is an identity smell{{if .Full}}, even when the ID is stable{{end}}.

A kind or edge earns its place only by answering a question the others cannot.
Prefer folding into an existing mechanism over adding one{{if .Full}} (pre-1.0: break
freely){{end}}.

## Say when not to build it

A mechanism that ACTS (a guard, a refusal, a cancellation, an auto-fix) is judged on
its wrong firings. Name the two cases its predicate cannot separate and the cost of
guessing each wrong. When it cannot separate them, do not build it{{if .Full}}, and say so rather
than shipping a check that fires on the wrong one{{end}}.

Wrong firings are the expensive direction: a gap gets noticed, while a check that
cries wolf teaches people to route around it{{if .Full}}, taking the real findings with it{{end}}.

## Verify the change

After restructuring, show the impact in graph terms. `{{cmd "graph diff"}} --rev <base>
-o markdown` lists the domain nodes and edges the change added, removed, or
altered{{if .Full}}, which suits a PR description{{end}}. It builds the base without
symbols, so rerun the cycle script for import edges. Then run `{{cmd "affected"}} ci`
to prove the affected projects still pass.

## Do not render the graph yourself

magus emits; it does not render. To look at structure, offer an export:
`{{cmd "graph export"}} --symbols -o json` (or `-o graphml`) for Gephi, yEd, or a
browser graph tool. Without `--symbols` the export has no package imports{{if .Full}};
do not hand-draw diagrams of what the graph already knows{{end}}.
