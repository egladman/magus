---
title: "ADR 0032: one dependency mechanism"
order: 32
description: A magusfile stops writing depends_on. Every edge is a typed reference written where it is used, always qualified by its project, this one included (self\lint, never a bare lint). A target edge is ctx.needs(proofread\build); an edge on another project's files is a read, proofread\sources() or proofread\file(glob), which orders nothing. The project graph is derived and printed read-only as upstream and downstream, each edge with the call that drew it. Same-name ordering ends, spells contribute the same typed references, and a written depends_on is refused by MGS1063 with the exact replacement.
tags: [adr, decision, dependencies, needs, magusfile, naming, project-graph, cache, affected, spells]
status: accepted
date: 2026-11-07
---

# ADR 0032: one dependency mechanism

## Context

Eli, 2026-11-07: "Naming is so important. It's the difference between somebody
confusing things and someone actually understanding." He decided that there is one
dependency mechanism, `needs`, always qualified and strongly typed; that a project
names itself too ("this project's lint", never a bare `lint`); that helpers may
shorten a call but keep the typed arguments underneath; and that `depends_on` stops
being writable and is not public API, with the project graph derived from `needs`.
This record settles the spellings that decision leaves open, and pushes back where the
evidence says one verb is one too few.

### Four ways to write an edge today

Measured on `kitchen-sink-again` at d91f85ec3, 2026-11-07, over the 20 magusfiles
outside `libs/gopherbuzz`:

| form                                     | where           | what it does                                                             | count                                          |
| ---------------------------------------- | --------------- | ------------------------------------------------------------------------ | ---------------------------------------------- |
| `"depends_on": [...]` in `magus\project` | project options | orders same-name targets, seeds affected, puts `dep:` lines in every key | 3 keys (root 13 entries, docs 12, console)     |
| `ctx.needs(alias\target)`                | target body     | runs the target first; folds into `depends_on`                           | 23 lines                                       |
| `ctx.needs(target)`, bare                | target body     | runs a same-project target first                                         | 125 lines                                      |
| `ctx.readsFiles([alias\file(glob)])`     | target body     | hashes another project's files; folds into `depends_on`                  | docs alone: 11 entries on the root in one call |

Two more channels exist outside magusfiles: a Go spell's `WithDependsOn(func(dir)
[]string)` (`spells/spell.go`, unioned in `WorkspaceRegistry.Apply`,
`internal/workspace/registry.go`), and a discovery spell's
`DiscoveredProject.dependsOn` (the TypeScript spell's `dependsOn` reads
`workspace:`, `file:` and `link:` ranges in `package.json`).

### The forms overlap, and the overlap is already refused twice

- MGS1062 refuses a cross-project `ctx.needs` that `depends_on` leaves out, so 10 of
  root's 13 entries and about 6 of docs' 12 restate a call already written in a body
  (the comments say so: "The rest are the libs whose targets these targets ctx.needs").
- MGS1061 refuses naming the root in `depends_on`, so an edge on the root has only the
  fold as its spelling. The same edge has two spellings for every project except one.
- A folded edge orders only the target it names; a declared one orders every same-name
  pair ([ADR 0027](0027-shared-prerequisites-run-once-in-stage-zero.md), amended
  2026-11-07). Whether `build` in docs waits on `build` in a library depends on which
  of two spellings carried the edge.

### The edges `depends_on` carries that no call carries

Read from the comments beside the keys, 2026-11-07:

| project | entry                                                                      | what it stands for                                                           |
| ------- | -------------------------------------------------------------------------- | ---------------------------------------------------------------------------- |
| root    | `proto`                                                                    | "orders proto's generate first" and puts a `.proto` edit in the affected set |
| root    | `libs/figure`                                                              | go-build embeds `figure.buzz`                                                |
| root    | `libs/embed`                                                               | `go.mod` replaces it with the local module, so its Go builds into magus      |
| docs    | `../proto`, `../libs/figure`, `../libs/kustomize`                          | API docs, the render and the figures read their files                        |
| docs    | `../libs/cmdline`, `diagnostics`, `embed`, `scipbuzz`, `xdg`, `gopherbuzz` | generators compile these Go libs into the binaries they run                  |

None of these is "run that project first". Each is either a target the downstream
needs (proto's `generate`) or files the downstream reads (a Go module, one `.buzz`
file). That split is the evidence the decision rests on.

### Bare names already collide

`docs/magusfile.buzz` aliases two imports for the same reason: `kustomize` because
"this name would shadow it", `render` because "the same-name `render` target would
shadow the import and null its members". A bare same-project target name and an
imported module share one namespace, and the loser reads as null.

### `needs` already means something else

The spell contract calls its input globs `needs` (`mgs_listRequiredGlobs (or needs)`,
[spells.md](../concepts/spells.md)), and [dependencies.md](../concepts/dependencies.md)
says the base step is seeded with "the union of every bound spell's `needs`". With
`needs` as the one dependency verb, the same word would name a call that runs a target
and a list of globs that runs nothing.

### The pending edge-digest and nested-glob design

On `untangle-root-ci-stages` at b1a207d8c, unpushed, Eli decided on 2026-11-07 that
every input glob stops at a nested project's boundary, as output globs already do;
that each edge, declared or folded, puts the upstream's source digest into the
downstream key, replacing the same-name fold at `internal/cache/cache.go:1674-1688`
that skips a missing key without saying so; that an edge whose upstream cannot be
resolved refuses with an MGS code; and that MGS1060 refuses a glob whose static prefix
lies in another project, redirecting to `ctx.needs(<alias>\<target>)` when that project
has a producing target and to `<alias>\file("<glob>")` otherwise. About 235 target
globs point into another project.

## Decision

The project graph is derived. Nothing in a magusfile declares a project edge; every
edge is a typed reference to another project, written where the dependency is used,
and the load unions them into the graph that `describe project`, `explain`, `graph`,
affected and the cache read. There are two kinds of reference and one verb per kind,
because the two kinds do different things.

### 1. A target edge is `ctx.needs`; a file edge is a read

| the downstream...                                    | writes                                                 | runs upstream work       | orders            | key gains                                |
| ---------------------------------------------------- | ------------------------------------------------------ | ------------------------ | ----------------- | ---------------------------------------- |
| needs a target's result                              | `ctx.needs([proofread\build])`                         | yes, once per invocation | that target first | the target's key                         |
| reads another project's sources, in one target       | `ctx.readsFiles([proofread\sources()])`                | no                       | nothing           | the upstream source digest               |
| reads some of another project's files, in one target | `ctx.readsFiles([figure\file("figure.buzz")])`         | no                       | nothing           | those files' bytes                       |
| reads another project's sources, in every target     | `"sources": [..., embed\sources()]` in `magus\project` | no                       | nothing           | the upstream source digest, in every key |

The whole-project edge ("my key includes your sources") is not `needs`. A need runs
something and waits for it; a read runs nothing and orders nothing, because sources are
hand-written and exist before any run. Putting both under one verb would make a reader
ask, of every `ctx.needs(x)`, whether it runs `x`. That is the confusion Eli's naming
priority is about, so this record keeps one verb per meaning rather than one verb.
Both verbs already exist; nothing new is added to read files, and `sources` already
takes a typed `alias\file(...)` entry under the pending MGS1060 redirect.

`proofread\sources()` is the upstream project's declared sources, stopping at its
nested projects, which is exactly the digest the edge-digest design puts in the key.
`file`, `sources` and `glob` (part 2) are reserved member names on a project import; a
target by one of those names is refused at load. No target in this workspace has one
(`magus query 'kind=target id=~:(file|glob|sources|self)$'`, no match, 2026-11-07).

An upstream file a target there produces is not a source. Reading it through
`\file()` is refused by the pending MGS1060, which names the producing target and the
`ctx.needs` to write instead, so a read never stands in for an order.

### 2. This project is `self`

Every magusfile has its own project bound as `self`, as a target body has `ctx`. A
same-project edge is `ctx.needs(self\lint)`; a glob over this project's targets is
`self\glob("*-generate", "!deploy-generate")`, replacing `ctx.glob`. The parameter of
`ctx.needs` is a target reference, which only `alias\name` produces, so a bare
`ctx.needs(lint)` passes a function where a target reference is expected and the Buzz
checker refuses it, naming `self\lint`
([The checker catches it](../design/principles/the-checker-catches-it.md)).

`self\lint` reads the same in every magusfile, so a target body copied into another
project still means "this project's lint", and a search for `self\` lists every
same-project edge. Helpers keep the typed references underneath: `ctx.needs` stays
variadic (`ctx.needs(self\lint, self\build, self\test)`), and `self\glob` returns
target references, never names.

### 3. The derived graph reads as `upstream` and `downstream`

`describe project` stops printing `depends_on:`. It prints `upstream:` and
`downstream:`, and each edge names the call that drew it, so the view reads as a
consequence of the magusfile rather than a field of it
([Every answer has an address](../design/principles/every-answer-has-an-address.md)).
Illustrative, for the root:

```text
upstream:
  proto        generate needs proto\generate              magusfile.buzz:<line>
  libs/figure  go-build reads figure\file("figure.buzz")  magusfile.buzz:<line>
  libs/embed   sources: embed\sources()                   go spell: replace ../libs/embed in go.mod
```

The JSON field is `upstream`, a list of `{project, via: [{target, call, site}]}`.
`explain project:<p>` prints project-to-project edges under the same two words, in
place of "depends on" and "required by". `graph deps` becomes `graph projects`, the
command that prints the `ProjectGraph`, and its `--upstream` flag, whose help says it
shows dependents, becomes `--downstream`, which is what it shows. The Go fields follow:
`types.Project.DependsOn` and `ProjectGraph.DependsOn` become `Upstream`, set only by
the load.

### 4. Same-name ordering ends

A run no longer orders `build` in one project after `build` in another because of a
project edge. A target waits on exactly the targets its body needs, and nothing else.
Where the old ordering did real work, the downstream writes the need: root's
`generate` writes `ctx.needs(proto\generate)`, which the `proto` entry's comment
already gives as its reason. A target that reads files another project's target writes
is refused by the pending MGS1060 with the `ctx.needs` that orders it, so a dropped
wait shows up at load, not as a race. `--race` keeps reporting an undeclared
cross-project read at run time (MGS4004) for what the load cannot see.

This completes ADR 0027's 2026-11-07 amendment, which already stopped folded edges
from ordering same-name pairs; its paragraph on declared waits is then stale and gains
an amendment pointing here.

### 5. Spells contribute the same typed references

A spell contributes edges in the two forms a magusfile writes, and no third:

- a file edge, as a typed foreign entry in the sources the spell contributes. The go
  spell turns `replace ../libs/embed` into `embed\sources()` on the project's sources;
  the TypeScript spell does the same for a `workspace:`, `file:` or `link:` package
  whose entry point is a source file;
- a target edge, as a need on a target the spell wires. When a linked package's entry
  point lies under a directory its own `build` writes, the TypeScript spell's wired
  `build` needs that package's `build`. This is the one place the old same-name
  ordering did work that no source read replaces, and the spell, which read the
  manifest, is the layer that knows it
  ([Abstract at the right layer](../design/principles/abstract-at-the-right-layer.md)).

Both are typed values (`types.SourceRef{Project, Glob}`, `types.TargetRef{Project,
Target}`), not path strings. `spells.WithDependsOn` and `DiscoveredProject.dependsOn`
are deleted; `describe project` names the spell and the manifest line in the edge's
`via`. To free the word, the spell contract's `needs` and `provides` become `sources`
and `outputs`, the names `magus\project` already uses for the same project-wide globs
([One-vocabulary rule](../glossary/one-vocabulary-rule.md)).

### 6. A written `depends_on` is refused by MGS1063

`depends_on` leaves the `magus\project` options type, so the checker would reject it
as an unknown key. MGS1063 gives that rejection its migration: one line per entry,
each naming what to write, worked out from the load it already does. Illustrative:

```text
[MGS1063] docs: magus\project no longer takes "depends_on"; the project graph is derived
from ctx.needs and reads. For each entry:
  "../libs/textsearch": delete it; target "lint" already needs textsearch\lint
  "../proto": target "api-docs-generate" reads a file proto's "generate" writes;
      write ctx.needs(proto\generate) in it
  "../libs/embed": no target reads it; to keep its digest in every key, add
      embed\sources() to "sources", with import "project/../libs/embed" as embed;
```

An entry with no call, no read and no producer gets the `sources` line, since that is
what `depends_on` gave it: a digest in every key. MGS1061 and MGS1062 are deleted; with
no declaration to compare a call against, neither has anything left to refuse. Their
pages go into `docs/retired.urls.lock`.

This is a deliberate break, made pre-1.0 with no shim, dual reader or alias
([Break now, hold still later](../design/principles/break-now-hold-still-later.md),
[A deliberate break never merges red](../design/principles/a-deliberate-break-never-merges-red.md)).
MGS1063 accepts nothing; it is a refusal with a remedy, so a magusfile that still
writes `depends_on` fails to load. The change that makes the break rewrites this
workspace's three `depends_on` keys and 125 bare `ctx.needs` lines in the same diff,
regenerates `magus-api.lock` and `std-api.lock`, records the break in
`changes/unreleased/`, and updates the SDK lock (`hack/lint/sdk-exports.buzz`) for the
`DependsOn` fields leaving. If a comparison gate reads the removal as a finding, the
change carries the `.deviations.jsonl` line, written by Eli.

### 7. It completes the edge-digest and nested-glob design

The pending design keeps a project-level edge list and makes each edge carry a digest.
This record removes the written list and keeps the digest rule, per reference:

- a `\sources()` read carries the upstream source digest, computed once per project per
  invocation and shared by every key that reads it
  ([Built for the largest monorepos](../design/principles/built-for-the-largest-monorepos.md));
- a `ctx.needs` carries the needed target's key, which already includes its project's
  sources;
- a `\file(glob)` read carries the matched bytes, so a docs target reading one
  `figure.buzz` no longer reruns on every `libs/figure` change. A `sources` entry keeps
  the coarse every-key digest for an edge that really is project-wide.

The boundary rule applies to `\sources()` and `\file()` as to any glob: docs'
`workspace\file("libs/**/*.go")` stops at each `libs/` project, and MGS1060 redirects
it to one `<lib>\sources()` per library. The redirect's `\file()` branch is this
record's read, and its `ctx.needs` branch is this record's target edge, so MGS1060 and
MGS1063 hand out the same two spellings. An unresolvable upstream refuses with the
code the pending design names, for either reference.

### State

| item                                                                                                                                                                             | State    |
| -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------- |
| `depends_on` removed from `magus\project`; MGS1063 refuses it, one remedy line per entry                                                                                         | proposed |
| MGS1061 and MGS1062 deleted; pages retired                                                                                                                                       | proposed |
| `self` bound in every magusfile; `ctx.needs` takes target references only; bare names refused by the checker                                                                     | proposed |
| `self\glob` replaces `ctx.glob`                                                                                                                                                  | proposed |
| `alias\sources()` beside `alias\file()`; `file`, `sources`, `glob` reserved target names                                                                                         | proposed |
| same-name ordering removed from the scheduler; root `generate` needs `proto\generate`                                                                                            | proposed |
| `describe project` and `explain` print `upstream` and `downstream` with `via`; `types.Project.DependsOn` and `ProjectGraph.DependsOn` become `Upstream`                          | proposed |
| `graph deps` becomes `graph projects`; `--upstream` becomes `--downstream`                                                                                                       | proposed |
| spells contribute `SourceRef` and `TargetRef`; `WithDependsOn` and `DiscoveredProject.dependsOn` deleted; the TypeScript spell wires `build` needs for build-output entry points | proposed |
| spell contract `needs`/`provides` renamed `sources`/`outputs`                                                                                                                    | proposed |
| [dependencies.md](../concepts/dependencies.md) rewritten around two verbs; `magus\needs` in the docs becomes `ctx.needs`                                                         | proposed |
| ADR 0027 gains an amendment pointing here                                                                                                                                        | proposed |

### Principles checked

- [Clear is better than clever](../design/principles/clear-is-better-than-clever.md):
  relies on it. One spelling per meaning, and the same spelling inside and outside a
  project.
- [The checker catches it](../design/principles/the-checker-catches-it.md): a bare
  name, a string path and an unknown key fail at check time; MGS1063 is that check's
  message, and its remedy uses only what the load already computes.
- [Delete before you add](../design/principles/delete-before-you-add.md): `depends_on`,
  MGS1061, MGS1062, `WithDependsOn`, `DiscoveredProject.dependsOn`, the same-name
  scheduler rule and about 16 restating entries go. Added: `self`, `\sources()`,
  MGS1063.
- [Behavior is code, facts are data](../design/principles/behavior-is-code-facts-are-data.md):
  relies on it. The edge is the call; the graph is a fact derived from code, not data
  written beside it.
- [Every answer has an address](../design/principles/every-answer-has-an-address.md):
  each derived edge prints the call and line that drew it.
- [No silent defaults](../design/judgment/no-silent-defaults.md): the same-name wait is
  behavior nobody wrote at the line it affects; it goes.
- [Break now, hold still later](../design/principles/break-now-hold-still-later.md) and
  [A deliberate break never merges red](../design/principles/a-deliberate-break-never-merges-red.md):
  the break is deliberate, has no shim, and carries its rewrite.
- [Abstract at the right layer](../design/principles/abstract-at-the-right-layer.md):
  a spell, which reads the manifest, contributes the edge; the core gains no language
  rule.
- Departure, decided here and flagged for Eli: his decision says one mechanism named
  `needs`. This record keeps one mechanism, typed references derived from where they
  are used, but two verbs, `needs` and reads, for the reasons in part 1 and the first
  alternative.

## Alternatives

Prior art below is input: it shows what each spelling costs elsewhere, never why to
pick one.

### The whole-project edge as `ctx.needs(proofread)`

One verb for everything, as Eli's decision reads literally. Lost because a need runs
and waits, and a project has nothing to run: a reader cannot tell whether
`ctx.needs(proofread)` runs every proofread target, a default one, or none. Evidence:
every root and docs entry that carries an edge no call carries is a read (the table in
Context), and none asks for a run. It would also need an import namespace to be a
first-class Buzz value, which was not checked in gopherbuzz. Reopens if Eli rules that
one verb outweighs the run/read ambiguity; then `ctx.needs(proofread\sources())` is
the spelling that keeps the ambiguity smallest.

### A new verb, `ctx.reads(proofread)`

Lost because `ctx.readsFiles` already takes `alias\file(...)`, so a second read verb
beside it is two spellings for one meaning. Reopens if reads grow a kind that is not a
file set.

### Project-level `"needs": [proofread]` in `magus\project`

A typed rename of `depends_on`. Lost because the same edge would again have a project
spelling and a body spelling, the overlap MGS1062 exists to police. Nx shows the cost:
`dependsOn` on targets, `implicitDependencies` on projects and inferred import edges
are three spellings of one graph, and its caret form `^build` is same-name ordering.
The project-wide case keeps one home, `sources`, which already holds project-wide
inputs. Reopens if a project needs an edge that is neither a read nor a target.

### This project as its own alias (`docs\lint` inside docs)

One spelling everywhere, inside and outside. Lost because an alias belongs to the
importer: `libs/proofread` is `proofreadlib` in the root and `proofreadmod` in docs,
the root is `workspace` from docs while its `name` key is `magus`, and the default
alias is a directory basename. A self-alias would be a fourth name for one project.
Reopens if import aliases become canonical per project.

### This project as `this`

Lost because `this` is Buzz's object receiver (`libs/gopherbuzz/checker.go` defines it
in method bodies), so `this\lint` inside an object method would mean two things.

### An explicit `import "project/." as self;` in every magusfile

Lost because all 20 magusfiles would carry the identical line, and leaving it out is an
error to write rather than a choice. Reopens if a magusfile ever needs to name itself
something else.

### Bazel-style `:lint` for the same project

Bazel and Buck2 spell a same-package label `:target`. Lost because it is a string
label, not a typed reference, and Buzz has no such token; it would be a second syntax
for one reference.

### Keep `depends_on` as the read-only name

Lost because a snake_case key in `describe project` reads as the key to write, which is
the misreading this record exists to stop.

### `dependencies`, `deps` or `needs` as the read-only name

`dependencies` and `deps` are the writable manifest field in npm, Cargo and Bazel, and
`deps` is an abbreviation. `needs` names the call, so a list printed as `needs:`
invites writing `"needs":` into `magus\project`. All lost on the same misreading.
`upstream` and `downstream` name a direction, not a field. Reopens if `upstream`
proves confusing beside the VCS meaning of a branch's upstream, which magus also
prints.

### Keep same-name ordering for edges a `sources` entry draws

Lost because a sources read is hand-written input that needs no order, and ADR 0027's
amendment found that counting same-name waits as prerequisites would move every
upstream's same-name targets ahead of stage one for no data reason. Reopens if a
workspace shows a same-name wait that neither a read nor a spell-contributed need
expresses.

### An ordering-only verb, `ctx.after(proto\build)`

Gradle's `mustRunAfter` orders without depending. Lost because an order with no data
flowing is a dependency left unsaid, or no dependency at all. Reopens with a target
whose side effect another target observes without reading a file, such as a service or
a deploy.

### Leave the spell contract's `needs` alone

Lost because one word would name a call that runs a target and a glob list that runs
nothing, on the same docs page. Reopens if the rename's cost to remote spells is
measured and found to outweigh it.

### Accept `depends_on` with a warning for one release

Lost under [Break now, hold still later](../design/principles/break-now-hold-still-later.md):
a warning is ignored, and MGS1063 already prints the rewrite. Reopening needs Eli's
yes, per AGENTS.md.

## Consequences

- A reader learns two verbs and one prefix: `ctx.needs` runs and orders, a read hashes,
  and every reference starts with a project, `self` included.
- A project's couplings are no longer listed in its declaration; `describe project`
  lists them, with their call sites. MGS1062's reason for being, that a reader of the
  declaration sees the couplings, moves to that view.
- Keys get finer: a target that reads one file of another project stops rerunning on
  every change there. The affected closure is unchanged at project level, since the
  graph is the union.
- Runs that relied on same-name ordering run in parallel. Each such dependence must be
  a need, and MGS1060 and `--race` are what catch a missed one.
- Every bare same-project `ctx.needs` (125 lines here) and every external magusfile
  that writes `depends_on` must change. The checker and MGS1063 each print the line to
  write.
- Remote spells that declare `needs`/`provides` or use `WithDependsOn` break and must
  move to the new contract.
- Out of scope: `types.Job.DependsOn` and the job store's dependencies are a different
  concept (order between jobs) and keep their names.

## Open questions

- Does Eli accept two verbs, or rule that one verb outweighs the run/read ambiguity?
  If one, the fallback spelling is `ctx.needs(proofread\sources())`.
- `console/magusfile.buzz`'s `depends_on` was outside this draft's read scope; its
  entries are not classified in Context.
- Does any TypeScript project here rely on same-name `build` ordering through a
  workspace package whose entry point is a build output? Not measured; it decides
  whether the TypeScript spell's target edge has a caller on day one.
- Is a Buzz import namespace a first-class value? It decides whether
  `ctx.needs(proofread)` was even writable.
- When does MGS1063 retire? It is not a shim, but it exists only for migration, and
  its retirement condition has to be something observable.
- MGS1063 is the next free code on this branch; take the number at merge.
- `explain` prints npm package edges under "depends on" too; whether those keep that
  label beside `upstream` for projects is not decided.

## Amendments

### 2026-11-07: accepted, with typed arguments

Eli accepted the names in parts 2 and 3 (`self`, `upstream` and `downstream`,
`graph projects`, `--downstream`) and settled part 1 from the argument types rather
than the verbs. There are two argument types, each with one effect:

```buzz
export object TargetRef { project: str, target: str }
export object InputRef  { project: str, glob: str, except: [str] = [] }
export object Context {
    extern fun needs(targets: [TargetRef]) > void;
    extern fun readsFiles(files: [InputRef]) > void;
}
```

`p\<target>` is a `TargetRef`; `p\sources()` and `p\file(glob)` both return `InputRef`,
so a whole-project edge is a larger file set, not a third kind. `ctx.needs` takes a list
because a Buzz declaration has no variadic parameter. A union parameter (`T | U`) does
not parse in gopherbuzz, and one `Dependency` protocol would leave ordering to each
element's kind, so both were rejected. Go keeps one `types.TargetRef`, which absorbs
`CrossTargetRef` and the bindings' `externalTarget`; `InputRef` always names its
project. The helper names (`readsFiles`, `file`) may change later; the types are the
decision.
