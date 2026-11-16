---
title: "ADR 0047: the magus module has namespaces"
order: 47
description: A Go package whose types cross into Buzz becomes one Buzz namespace under the host module, so two packages may each declare a Step. buzzgen derives the namespace from the package, writes one declaration file for it and imports it where a signature names it. The prefix renames in .buzzgen.yaml are deleted. Functions keep the two shapes they have, a member group and a handle, and what has no scoped Go package yet stays at the top level.
tags: [adr, decision, buzz, gopherbuzz, codegen, host-modules]
status: proposed
date: 2026-11-12
---

# ADR 0047: the magus module has namespaces

## Context

Eli, 2026-11-12: "stop dumping everyone in the single magus buzz module, or at the very
least start having methods inside the magus module, so something like
magus\agent().step". Measured at 20b25e6b2 on 2026-11-12 with `awk` counts over the
generated files and `magus refs` text counts.

### One flat namespace

- `internal/spell/gen/decls/magus.buzz` (3,064 lines) exports 308 types, 53 enums and
  255 objects. The other 45 declaration files export 79 between them.
- `internal/spell/builtins.go` adds four more by hand: `Exec`, `Context`, `TargetRun`
  and `Run`.
- The same file exports 22 top-level functions (`:2957-2978`) and 10 member groups
  holding 46 functions (`:2980-3063`).
- Every type is declared before any import runs, so a magusfile reaches it with no
  qualifier: `fun (req: CommandRequest) > GuardVerdict` (`magusfile.buzz:20`).
  `DeclareMagusTypes` says why the order matters: mirrors with generic names (`Node`,
  `Path`, `Graph`) must stay beneath a program's own type of that name
  (`internal/spell/builtins.go:312`).

### The collisions are already paid for in config

- A mirrored type lands in the module whose signature reaches it. `magus` reaches most
  of them, so a scoped Go package does not get a scope in Buzz.
- `.buzzgen.yaml` (172 lines) holds 107 `name` settings. 69 of them prepend the Go
  package's name to keep two types apart: 22 `Diff*` for `types/diff`, 5 `Trail*` for
  `types/trail`, 4 `Agent*` for `types/agent` and 38 `Queue*` for
  `internal/queue/types`.
- Two Go types named `Step` reach the module: `diff.Step` as `DiffStep` and
  `agent.Step` as `AgentStep` (`.buzzgen.yaml:22`, `:68`). Four more names end in
  `Step`: `PathStep`, `ChainStep`, `TierGateStep` and `CharmTraceStep`.
- The renames are uneven. `agent.StepRequest` crosses as `StepRequest` and holds an
  `AgentStep` (`magus.buzz:2413`, `:2425`).
- Inside the root `types` package the same thing is done in Go names: 30 mirrors start
  with `Job`, 24 with `Diff`, 18 with `Knowledge` and 10 with `Conformance`.
- ADR 0035's amendment of 2026-11-08 already decided that a Buzz name is the Go type's
  own name and that its module is its Go package. It did not say how a package under
  `magus` is spelled, so the three packages scoped since then took prefixes.

### What already scopes a name

- A member group is a static object: `magus\guard.command`, `magus\describe.file`,
  `magus\harness.provider`. `std.Namespace` declares it (`std/module.go:159`).
- A handle is a record a function returns, with methods that call Go:
  `magus\workspace().jobs()`. 49 methods sit on 8 records (`magus.buzz:2907-2955`).
- A slash import path already exists: `import "magus/figure";` binds `figure\Figure`,
  and `import "magus/spell/go";` binds `go`. `import "magus/` occurs 615 times in 399
  files.
- None of the three scopes a type. An object declares fields, static fields and
  methods and nothing else (`libs/gopherbuzz/checker_typenames.go:53`), and a slash
  path binds under its last segment, where `diff` is already a host module
  (`internal/spell/gen/decls/diff.buzz`).

### What upstream Buzz and gopherbuzz offer

- Upstream's import guide (buzz-lang.dev, read 2026-11-12) says "A namespace is any
  number of identifiers separated by `\`." A script that exports a symbol must open
  with a `namespace` statement. An import binds the declared namespace, with any prefix
  it shares with the importing script removed. `import "x" as y` renames it and
  `as _` erases it.
- gopherbuzz parses `namespace a\b\c` (`libs/gopherbuzz/parser.go:689`) and binds an
  import under the full declared path. Two imports sharing a first segment merge, and
  a namespace declared twice is refused (`session_test.go:383-430`). The shared prefix
  is removed as upstream removes it (`session.go:2482`).
- No generated declaration file declares a namespace or imports another: 0 of 46.
- The comment on `std.Namespace` says Buzz has no nested namespace
  (`std/module.go:152`). The tests above show that is no longer true.

## Decision

A Go package is a Buzz namespace. The rule is stated with invented names first and
mapped to magus last.

### 1. One package, one namespace

A workspace's host module is `shop`. Its Go types live in `shop/types`, and two scoped
packages each declare a `Step`:

```go
package order // shop/types/order
type Step struct{ SKU string }

package cart // shop/types/cart
type Step struct{ Line int }
```

Each package is one Buzz module. Its import path is the host module's path, a slash
and the Go package name, and it declares the matching namespace:

```buzz
import "shop";
import "shop/order";
import "shop/cart" as cart;

fun next(step: shop\order\Step) > cart\Step !> any {
    return shop\checkout(step);
}
```

- A type keeps its Go name. No prefix is added and none is removed.
- A script imports each namespace it names. Importing `shop` does not bring
  `shop\order` with it, as upstream imports are per file.
- An unimported namespace is already BZZ1009, which names the import line to add.

### 2. buzzgen derives it

This amends ADR 0035. Membership stays reach: no type and no namespace is listed.

- The `module` setting may hold a slash path. Its default for a package is the config's
  top-level `module`, a slash and the Go package name. The package that sets `module`
  itself is the root, so `shop/types` sets `module: shop`. A type's own `module` still
  wins, as `HTTPOptions` is scoped to `http` today.
- A reached type is written to its package's module, whichever host module reached it.
  The rule that bundles a mirror into the module that returns it is deleted.
- Each module is one declaration file, `<decls>/shop/order.buzz`, and it opens with
  `namespace shop\order;`. `shop.buzz` opens with `namespace shop;`.
- A file whose signatures name another module's type imports it and writes the type
  qualified. In `shop.buzz` that is `import "shop/order";` and `order\Step`.
- Two types taking one name in one namespace are already an error naming both roots.
  Two packages deriving one namespace are the same error, and it names the `module`
  line to add.
- A namespace segment that is also a member of its parent module is refused, naming
  both. `shop\order\Step` and a function `shop\order(...)` cannot both exist.
- Decoders, encoders and the runtime record definitions are keyed by the qualified
  name.

### 3. Functions keep two shapes

- A member group stays a static object, `magus\guard.command`. It scopes functions
  that share no state with the caller.
- A handle stays a record with methods, `magus\workspace().jobs()`. It is for a value
  that carries state.
- No accessor is added to reach a namespace. `magus\agent().step` is not built; see
  Alternatives.
- A host function written for a scoped package from now on is declared in that
  package's module, `shop\order\total(...)`, with `Module.Path` set as `encoding/json`
  sets it. No new member group is added for it.

### 4. What stays at the top level

- The 22 functions and the 10 member groups.
- `Context`, `Exec`, `Run` and `TargetRun`, which every target signature names.
- Every type still declared in the root Go package. Its count falls as ADR 0035's
  planned moves into scoped packages happen, and this record schedules none of them.
- A type scoped to another host module by `module`, such as `fs\WalkStep`.

### 5. The mapping in magus

| Go package    | Namespace     | Before                         | After                                |
| ------------- | ------------- | ------------------------------ | ------------------------------------ |
| `types/agent` | `magus\agent` | `AgentStep`, `StepRequest`     | `magus\agent\Step`, `…\StepRequest`  |
| `types/diff`  | `magus\diff`  | `DiffFile`, `DiffStep`         | `magus\diff\File`, `magus\diff\Step` |
| `types/trail` | `magus\trail` | `TrailMark`, `SessionOptions`  | `magus\trail\Mark`, `…\ReadOptions`  |
| `types`       | `magus`       | `Job`, `Graph`, `GuardVerdict` | unchanged                            |

- The config's top level sets `module: magus`. `types/image` sets `module: ""`, so
  `image\Platform` keeps its spelling beside the `image` functions.
- 32 `name` lines are to be deleted from `.buzzgen.yaml`: 22 for diff, 6 for trail and 4
  for agent. The 4 for agent are deleted; see the State table for diff and trail.

### 6. The deliberate break

- Every renamed type changes spelling with no alias and no dual reader. The API locks
  are regenerated in the same pull request, and a changelog fragment records the
  break.
- The change is built in its own pull request, ahead of the one-dialect change and the
  `magusfiles/` move (Eli, 2026-11-12). A magusfile that names a moved type gains one
  import line now.
- The callers are found from the symbol index, not by hand. Eleven of the renamed names
  were sampled with `magus refs`, and each is referenced only by the generated file.
  The index was stale at the time, so the build job measures again before it edits.
- Docs, skills and the examples under `std/examples` are regenerated or swept in the
  same pull request.

| Item                                                                          | State                          |
| ----------------------------------------------------------------------------- | ------------------------------ |
| gopherbuzz parses and binds a multi-segment declared namespace                | done                           |
| The checker resolves `a\b\T` in a type annotation and in a declaration file   | done                           |
| `module` takes a slash path and defaults from the Go package                  | done                           |
| One declaration file per namespace, each opening with `namespace`             | done                           |
| A declaration file imports the namespaces its signatures name                 | done                           |
| A segment that is also a parent member is refused                             | done                           |
| `types/agent` mirrors under `magus\agent`; its 4 prefix `name` lines deleted  | done                           |
| `types/trail` mirrors under `magus\trail`; its 6 prefix `name` lines deleted  | done                           |
| `types/diff` mirrors under `magus\diff`; its 22 prefix `name` lines deleted   | not built (Diagnostic, Origin) |
| `Check`, `Evidence`, `Tier` and kin move to `types/check` as `magus\check`    | done                           |
| `ImpactCoverage` and `ImpactProject` move to `types/impact` as `magus\impact` | done                           |
| `VCSCheckpoint` moves to `types/vcs` as `magus\vcs`                           | done                           |
| `ReleaseStage` moves to `types/release` as `magus\release`                    | done                           |
| `Diagnostic` stays in `libs/diagnostics` and in `magus`                       | done (no module line)          |
| The rule that bundles a root type into the module returning it deleted        | not built (root types keep it) |
| The stale comment on `std.Namespace` corrected                                | done                           |

What the build found, 2026-11-12:

- The checker read `a\b\T` by its last segment only, so two nested `Step` types were one
  type to it. A declaration module that declares a nested namespace is now collected
  under that namespace, binds under it alone and not under its last segment, and is
  visible to a file only through that file's own import, as upstream binds it. Type
  identity is still the bare name in `types.Compat`, so `shop\order\Step` passes where
  `shop\cart\Step` is wanted. That predates this record: `magus\CommandOptions` and
  `vcs\CommandOptions` compare the same way.
- `types/diff` cannot move yet. Its fields name root types (`types.Check`,
  `types.ImpactCoverage`, `types.VCSCheckpoint` and others), and `magus` functions return
  `diff.Diff`, so the `magus` and `magus\diff` declarations would import each other. gopherbuzz
  refuses a cycle between declaration modules. The Go packages have no cycle because the
  `magus` host module is `std`, not `types`.
- `types/trail` cannot move yet. `trail.Trail` is a `magus` handle whose methods call the
  `magus` module's private externs, which a separate declaration file cannot reach.
- Deleting the bundling rule would move `fs\FileInfo`, `vcs\Commit` and the other root
  types bundled into a host module out of it, a break part 5's table does not list. Root
  types keep it.

What the second build found, 2026-11-13:

- `types/trail` moved with no extern moved. The handle is `magus\Session`, a root type
  whose methods call `magus__Session__trail` and its siblings in `magus.buzz`. `Trail`
  is a plain record. `magus.buzz` imports `magus/trail`, and `magus/trail` imports
  nothing. The note above named the wrong record.
- `types/diff` is not moved. Eli decided on 2026-11-13 that the root types its fields
  name move to `types/diff` or to a lower package both import. The mirrors reach 10
  root types: `Check`, `CheckStatus`, `Evidence`, `Diagnostic`, `ImpactCoverage`,
  `ImpactProject`, `TierEvidence`, `Tier`, `ReleaseStage` and `VCSCheckpoint`.
- None of the 10 can move to `types/diff`. Root `types` uses each one, and `types/diff`
  imports `types`, so Go would refuse the cycle.
- A lower package is a new name and a break the table does not list. Each of the 10
  changes its Buzz spelling, and `types.Check` alone is named in 81 Go files.

What the third build found, 2026-11-13:

- Eli split the 10 by meaning: `types/check` takes `Check`, `CheckStatus`, `Evidence`,
  `TierEvidence` and `Tier`, and `types/impact` takes `ImpactCoverage` and
  `ImpactProject`. Both are built. buzzgen derives `magus\check` and `magus\impact`
  with no config line.
- The stutter rule refuses `check.CheckStatus`, so the Go names drop the package's
  name: `check.Status`, `check.OK`, `impact.Coverage` and `impact.Project`. The Buzz
  names follow the Go names.
- `Diagnostic` has an owner, `libs/diagnostics`. `types` already imports it, and it
  imports nothing of magus. Its parent `libs` is not listed in `.buzzgen.yaml`, so no
  namespace derives for it, and it would stay in `magus` until its package sets
  `module`.
- `VCSCheckpoint` has no owner below `types`. The `vcs` package imports `types`, and
  `types/job.go` and `types/magus_handle.go` name the checkpoint.
- `ReleaseStage` has no owner below `types`. `internal/release` imports `types` and
  the interpreter. `NewestReleaseStage` reads `types.VCSTag`, so it stays in `types`
  unless `VCSTag` moves with it.
- The depguard list `types-pure-domain` lets `types` import `types/agent` and no other
  scoped package, so it needs `types/check` and `types/impact` added.
- The checkout's own `./magus` predates `magus\check`. `hack/policy/mandate.buzz`
  imports it, so that binary cannot load the magusfile until it is rebuilt.

What the fourth build found, 2026-11-13:

- Eli picked the rest on 2026-11-13. `VCSCheckpoint` moves to a pure `types/vcs` as
  `magus\vcs`, and `VCSTag` stays in `types`. `ReleaseStage` moves to a pure
  `types/release` as `magus\release`. `Diagnostic` stays in `libs/diagnostics` and in
  plain `magus`, with no `module` line.
- The stutter rule refuses `vcs.VCSCheckpoint` and `release.ReleaseStage`, so the Go
  names are `vcs.Checkpoint` and `release.Stage`.
- `NewestReleaseStage` reads `types.VCSTag`, so it stays in `types` and returns a
  `release.Stage`. `ParseCheckpointToken` stays in `types` beside `VCSTag`.
- `types/diff` is not moved yet. Deleting its 22 `name` lines makes the provider op
  records fail to generate: `internal/spell/target.go` names `ReviewTarget` in a
  `go:generate` line, the mirror is now `magus\diff\ReviewTarget`, and the spell contract
  files under `internal/spell/gen/types` import no namespace. Those records and
  `cmd/magus-utils/provider_ops.go` move with it.
- A script that builds a `magus\Change{}` literal fails with `unknown object type
  "Checkpoint"`: the record's default `magus\vcs\Checkpoint{}` is bound by neither the
  session nor the records `bindMagusRecords` runs.

What the fifth build found, 2026-11-13:

- The `magus\Change{}` literal constructs. A default compiles at the literal, so
  `DeclareMagusTypes` declares each namespace the magus declarations import with
  gopherbuzz's `Session.DeclareNamespaceTypes`, qualified only, and `bindMagusRecords`
  binds those objects under their qualified names. Running the imports instead bound
  `magus` as a bare namespace, which an aliased file import then read as the module.
- `types/diff` still names `types.Diagnostic` and `types.Origin`. Eli keeps
  `Diagnostic` in plain `magus`, so a `magus\diff` declaration would import `magus`
  while `magus` imports it, the cycle gopherbuzz refuses. The move waits until both
  have an owner below `types`, and the 22 `name` lines stay in `.buzzgen.yaml`.

### Principles checked

- [One source, every door](../design/principles/one-source-every-door.md): the Go
  package clause is the one source of a type's scope. The config stops restating it as
  a prefix.
- [Delete before you add](../design/principles/delete-before-you-add.md): 32 config
  lines and the bundling rule are deleted. Nothing is added to the language.
- [Clear is better than clever](../design/principles/clear-is-better-than-clever.md):
  one rule, the package is the namespace, and a reader of either language finds the
  other's name without a table.
- [The checker catches it; the runtime is the backstop](../design/principles/the-checker-catches-it.md):
  a collision fails generation, and a namespace a script did not import fails the
  check.
- [No silent defaults](../design/judgment/no-silent-defaults.md): the derived default
  is refused, not adjusted, when two packages reach one namespace or a segment meets a
  member.
- [Behavior is code, facts are data](../design/principles/behavior-is-code-facts-are-data.md):
  the config keeps only what Go cannot say, the root's `module` and the rare override.
- [Break now, hold still later](../design/principles/break-now-hold-still-later.md) and
  [A deliberate break never merges red](../design/principles/a-deliberate-break-never-merges-red.md):
  the spellings change before 1.0 with no shim, and the pull request carries its locks
  and its changelog fragment.
- [Capability is not a reason](../design/principles/capability-is-not-a-reason.md): the
  failure removed is measured, 69 prefix renames and a name that had to be invented for
  each new package.
- [Abstract at the right layer, or not at all](../design/principles/abstract-at-the-right-layer.md):
  the rule lives in buzzgen, which every embedder runs, and names nothing of magus.

## Alternatives

### An accessor object, `magus\agent().step`

Eli's first spelling: a function returns an object whose members are the package's
types and functions. It lost because an object cannot hold a type. `ast.ObjectDecl`
carries fields, static fields and methods (`libs/gopherbuzz/checker_typenames.go:53`),
and an annotation takes a type name, not an expression, so
`fun (req: magus\agent().StepRequest)` cannot be written. The collision that started
this was between two types. For functions the shape already exists as a member group.
Reopen when upstream Buzz lets an object declare a nested type.

### Keep one namespace and prefix the names

Today's shape: `name: AgentStep`. It lost because every new type in a scoped package
needs a config line, and 69 are there already. ADR 0035's amendment of 2026-11-08
already judged a Buzz name carrying its Go prefix to read as generated code. Reopen if
the checker cannot resolve a nested namespace without a language change upstream does
not have.

### A top-level module per package, `agent\Step`

`import "magus/agent";` binding under its last segment, as `magus/figure` binds
`figure`. It lost on two measurements. `diff` is already a host module with 3 functions,
and two modules may not share a name (`std/module.go:401`). Upstream binds the declared
namespace, not the path's last segment, so the spelling would not survive the
one-dialect change. A script that wants the short name writes
`import "magus/agent" as agent;`. Reopen if the one-dialect change keeps last-segment
binding.

### `import "magus"` brings every namespace

One import line, every type reachable. It lost because it keeps the cost the flat
module has: every declaration is parsed and collected whether a script names it or not,
308 types today. Upstream has no import that carries another file's namespace with it.
Reopen if scripts are measured importing most namespaces most of the time.

### Turn the member groups into namespaces, `magus\guard\command`

One shape for functions and types. It lost because it removes no collision and
changes many call sites: `magus\guard.` occurs 884 times in 278 files,
`magus\describe.` 222 times in 124 and `magus\log.` 139 times in 36. A group's methods
are also bound by the runtime for each open workspace, which a declaration file does
not do. Reopen when a group's types move to a Go package of the group's name. Part 2
then refuses the pair, and the group becomes the namespace.

### List each type's namespace in the config

Set `module` on every type. It lost because it is hand-listed membership, which
ADR 0035's amendment of 2026-11-11 deleted after one new type took four edits. Reopen
if a package is found whose types belong in two namespaces.

## Consequences

- A new scoped Go package gets a Buzz namespace with no config line, and its type
  names need no prefix.
- A script names more imports. One that annotates a step rule imports `magus/agent`.
- The generated declarations become a tree of files. Review of a mirror change reads
  one small file instead of a 3,064-line one.
- The top-level module shrinks only as Go types move to scoped packages. Until then
  `Job*` and `Knowledge*` stay where they are.
- A scoped package cannot take the name of a top-level function. `job`, `graph`,
  `change`, `history`, `session` and `workspace` are functions today, so a package for
  job records needs another name or the function does.
- `std.Namespace` keeps its Go name while meaning a member group. The word now has two
  meanings in the tree until one is renamed.
- Sub-namespace types are never reachable unqualified, so they cannot shadow a
  program's own type and need no eager declaration.

## Open questions

- Which package below `types` owns `Diagnostic` and `Origin`, so `types/diff` can move
  to `magus\diff` without the declarations importing each other.

- How a handle whose record lives in a nested namespace reaches its host's externs. No
  moved package has needed it yet.
- Whether upstream lets a function `shop\order` and a namespace `shop\order` coexist.
  If it does, the refusal in part 2 is narrower than it needs to be.
- Whether `magus/figure` and `magus/spell/*` declare `namespace magus\figure` in the
  one-dialect change, which would change `figure\Figure` to `magus\figure\Figure`.
- Whether the queue provider's 38 `Queue*` names drop their prefix by the same rule.
  They are written to a different output, and its scripts read it under another module.
- What `std.Namespace` is renamed to. A package rename needs Eli's pick.
- Whether the unqualified reach of top-level types in a magusfile survives the
  one-dialect change. This record leaves it as it is.
