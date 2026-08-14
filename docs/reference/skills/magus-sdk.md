---
title: magus-sdk
description: "Help a Go developer consume magus as a library (import \"github.com/egladman/magus\") instead of shelling out to the CLI, and audit whether the SDK actually serves them."
tags: [agents, skills, magus-sdk]
skill_full_bytes: 13323
skill_simple_bytes: 12886
---

# magus-sdk

Help a Go developer consume magus as a library (import "github.com/egladman/magus") instead of shelling out to the CLI, and audit whether the SDK actually serves them. Use when someone wants to call Open/Inspect/Run from their own Go program, embed magus's workspace model in another tool, or asks "can I use magus without the binary". Also use to audit the SDK surface itself - whether a type is exported, a concept is reachable without the CLI, and whether a package boundary is deliberate or accidental. Do NOT use for CLI usage (magus-run, magus-query) or for editing magus's own source (magus-architecture-review).

Install it, rather than copying from this page:

```sh
magus agent install .claude/skills   # writes both forms below
```

An installed copy carries a provenance stamp, so `magus graph verify` can tell you when a magus upgrade has made it stale. Text copied from this page carries none.

## What an installed copy carries

`magus agent install` writes this frontmatter above the body. `magus graph verify` reads it to report whether your installed skills are current.

| field | value |
| --- | --- |
| `license` | `GPL-3.0-or-later` |
| `compatibility` | `any-agent` |
| `source` | `magus` |
| `agent-skill-version` | `37` |
| `knowledge-schema-version` | `9` |
| `skill-content` | `430a06d637fe` |
| `skill-variant` | `full` |

The `skill-content` digest is shared by both permutations below, so they version together: a magus upgrade makes both stale at once, never one silently.

## Full form

Every mechanical step spelled out, plus the rationale for each. Installed as the `<name>-full` twin: loaded by name rather than always, so a reader who needs the long form can ask for it without every session carrying it.

````markdown
# Consuming magus as a Go library

The reader this skill serves has never run `magus` and does not know its
subcommands. They found `github.com/egladman/magus` on pkg.go.dev or in an
import line and want to call it from their own Go program. Ground every
answer in the actual exported surface (`magus.go`, `types/repository.go`,
`describe.go`, `types/describe.go`, `run.go`, `knowledge.go`,
`project/impact/impact.go`) - never in what the CLI does, which this reader
cannot see.

## Before anything else: can they even `go get` it?

VERIFIED against the real published module at the real latest tag
(`go get github.com/egladman/magus@v0.3.0` from a clean module, no local
replace): it fails outright. `go.mod` requires
`github.com/egladman/magus/libs/gopherbuzz` and `.../libs/diagnostics` - nested
modules with their own `go.mod` - at `v0.0.0`, and no `libs/gopherbuzz/vX.Y.Z`
or `libs/diagnostics/vX.Y.Z` tag exists in the repo (only root tags: v0.1.0 - v0.3.0).
The root module's own `go.mod` resolves them via LOCAL replace directives
(`replace github.com/egladman/magus/libs/gopherbuzz => ./libs/gopherbuzz`);
replace directives are not transitive, so a downstream consumer inherits
none of that and hits `unknown revision libs/gopherbuzz/v0.0.0`.

Until those nested modules get their own tags, the only working install is:
clone the repo (or vendor `libs/gopherbuzz` and `libs/diagnostics` from it), and add
matching replace directives to the consumer's own `go.mod`:

```go
require github.com/egladman/magus v0.3.0

replace github.com/egladman/magus/libs/gopherbuzz => /path/to/magus/libs/gopherbuzz
replace github.com/egladman/magus/libs/diagnostics => /path/to/magus/libs/diagnostics
```

Tell the reader this plainly before anything else. A worked example that
compiles for you (inside the module, or via a `replace` on the root) but
silently fails for them with no local checkout is worse than admitting the
gap.

## Entry points

| Call | Returns | Cache | Use when |
|---|---|---|---|
| `magus.Open(ctx, root, opts...)` | `*magus.Magus` | builds one | the caller will `Run` targets or read cached results |
| `magus.Inspect(ctx, root, opts...)` | `types.WorkspaceRepository` | none | pure introspection: list/describe/classify, never execute |

Both discover the workspace root's projects and evaluate magusfiles the same
way (`Open` calls the same `load()` `Inspect` does, then additionally opens
the on-disk cache) - the difference is purely whether a cache gets built, not
what gets discovered. `Inspect` returning the narrow
`types.WorkspaceRepository` interface rather than the concrete `*Magus` is
itself a hint: an introspection-only caller should code against the
interface, and a caller who later needs `Run` should switch to `Open` and get
the concrete type, not type-assert their way there.

`magus.FindRoot(dir)` walks up from `dir` (or cwd) to locate the workspace
root before calling either constructor, the same walk the CLI does.

## The interface hierarchy: depend on the narrowest role

`types.WorkspaceRepository` is `WorkspaceReader + TargetExpander +
AffectedComputer + Inspector`, embedded, not one flat interface. `*Magus`
(from `Open`) and the value `Inspect` returns both satisfy the whole thing,
but a function that only reads project facts should take
`types.WorkspaceReader`, not `types.WorkspaceRepository` -
`types/repository.go` says this outright: "Prefer the narrowest embedded role
a consumer actually uses."

| Role | Methods | Answers |
|---|---|---|
| `WorkspaceReader` | `Root`, `All`, `Get`, `Graph`, `VCSOptions`, `Where` | what projects exist, and the project dependency graph |
| `TargetExpander` | `ExpandPath`, `ExpandCwd`, `ExpandAffected` | which concrete `path:target` pairs a target pattern names |
| `AffectedComputer` | `Affected`, `AffectedFromPaths` | which projects a VCS changeset touches |
| `Inspector` | `List*`, `Evaluate*`, `ClassifyFiles`, `TargetGraph`, `Workspace` | see the axis below |

A function that prints project names needs only `WorkspaceReader`; widening
its parameter to `WorkspaceRepository` because that is what `Open` returns is
the over-coupling this hierarchy exists to prevent - it forces every future
caller to construct or stub the whole repository just to satisfy a signature
that only reads `Root()` and `All()`.

## The List / Evaluate / Classify axis

This is the SDK's organizing idea (`types/repository.go`'s `Inspector` doc
comment states it directly): `List*` enumerates a DECLARATION, cheap, no
resolution. `Evaluate*` RESOLVES one - spells bound, claims applied, charms
patched in - and costs more. `ClassifyFiles` and `TargetGraph` are their own
verbs because neither fits the List/Evaluate split cleanly.

| Method | Reads | Cost |
|---|---|---|
| `ListProjects` | declared project facts (project-relative globs) | cheap |
| `ListTargets` | target name -> spell/project vocabulary | cheap |
| `ListCharms` | inverse charm index across every project | most expensive `Inspector` method (renders every charm x target x spell) |
| `EvaluateProjects` | resolved spells, workspace-rooted globs | resolves every project |
| `EvaluateTarget(ctx, t)` | full dispatch plan for one `path:target` | resolves one target |
| `ClassifyFiles(ctx, paths)` | which project owns/declares each path | pure glob lookup, cheap even for a whole dirty tree |
| `TargetGraph(ctx)` | the `ctx.needs` DAG, read statically from magusfile source | never executes a target body |

`ProjectEntry.Sources` (from `ListProjects`) and `EvaluatedProject.Sources`
(from `EvaluateProjects`, via its embedded `ProjectEntry`) are the SAME FIELD
NAME carrying DIFFERENT representations - declared, project-relative globs in
the first case; resolved, workspace-rooted globs (joined against the project
path, magusfile globs folded in) in the second. Reading one where the other
was intended silently mismatches every glob it feeds. This is documented
exactly once, in the field comment on `ProjectEntry.Sources` in
`types/describe.go` - read it before writing code that consumes both.

## ctx and cancellation

Every `Inspector` method takes `ctx` first and returns `error` last,
including the read-only `List*` calls. A cancelled walk returns an error
instead of a truncated slice - `describeCancelled` in `describe.go` names the
walk and how far it got (`"describe projects: cancelled after 2 of 8: context
canceled"`), and wraps `ctx.Err()`, so `errors.Is(err, context.Canceled)` and
`errors.Is(err, context.DeadlineExceeded)` both hold. VERIFIED: cancelling
before calling `ListProjects` on a one-project workspace produces exactly
that message and `errors.Is` returns true.

Never treat an empty or short `List*`/`Evaluate*` result as "the workspace has
nothing" without checking the error first. The reason is stated directly in
describe.go: a partial inventory reporting `Count: 3` is indistinguishable from a
workspace that genuinely has three projects, so silently truncating would be a
wrong answer wearing a right answer's shape.

## Two different graphs, easy to conflate

- `WorkspaceReader.Graph()` returns `*types.Graph`: the PROJECT dependency
  graph (project -> project, from `depends_on`).
- `Inspector.TargetGraph(ctx)` returns `types.TargetGraphOutput`: the TARGET
  graph (target -> target, from `ctx.needs`), within and across projects,
  read statically from magusfile source - it never runs a target body, so it
  sees both arms of a runtime branch and reports a dependency cycle if one
  exists.

A caller doing scheduling or impact analysis typically holds both: `Graph()`
for "does project A depend on project B", `TargetGraph()` for "does target
`lint` in project A depend on target `build` in project B". Do not reach for
one when the question is about the other's granularity.

## Sharp edges (verified, not folklore)

**`types.Target` is dual-role.** As a work-unit, `Path`/`Name`/`Charms`/`Files`
identify which `path:target` to run. As a policy bag (`Project.TargetPolicies`
values, `EvaluatedTarget.Policy`), only `SkipCache`/`Exclusive`/`Slots`/
`FailOnDrift`/`RetryOnVolatile` are meaningful - the other 6 of its 11 fields
sit unset and must be ignored (see `types/target.go:95-101` for the type's own
disclaimer). A function that receives a `types.Target` needs to know which
role it is playing before reading any field.

**The `Entry`/`Output`/`Report` suffix split needs the ~20-line comment at
the top of `types/describe.go` to make sense**, and most of it is a naming
RULE, not vocabulary: `Entry` is added to a type name only when the bare name
would collide with an existing type (`ProjectEntry` because `types.Project`
exists; `Charm` has no suffix because nothing else claims that name).
`Output` means "the `Inspector` method itself returns this shape"
(`ProjectsOutput`, `TargetGraphOutput`). `Report` means "rebuilt at the render
edge from a plain slice the method actually returned" (`FileReport`,
`CharmReport`) - `ListProjects`/`EvaluateProjects` are the deliberate
exceptions, still returning their `*Output` type directly because they carry
a real `Workspace` field a `{definition, count, items}` envelope cannot
derive. Guess at this pattern instead of reading the comment and you will
misname a type you add.

**Buzz magusfile evaluation is not reachable from outside this module.**
VERIFIED end to end: a separate module importing `github.com/egladman/magus`
(via `replace`, matching the install workaround above), pointed at a
workspace whose only project declaration is a `magusfile.buzz` with an
`export fun build` target, produces `ListTargets` returning ONLY the
hardcoded `ci` anchor and `ListProjects` reporting `resolvedSpells: 0` -
`build` never appears as a runnable target, and no spell is attached. Reason:
magusfile evaluation is gated on `interp.Available()` (`magus.go`'s `load`),
which is only true when `internal/interp/engine/buzz` and
`internal/interp/bindings` are blank-imported - and those are `internal/`
packages under this module, structurally unreachable from any other module's
import graph. Only `cmd/magus` (built inside this repo) links them
(`cmd/magus/packs_interp.go`). `TargetGraph` still works for a Buzz
magusfile in this situation (it reads the source statically via the exported
`libs/gopherbuzz` parser, not the internal engine) - so an external caller
can SEE the target graph a magusfile declares but cannot make magus DISPATCH
it. `forEachSpell` in `magus.go` returns `nil` immediately when a project has
zero resolved spells, so calling `Run` against such a target does not error;
it silently does nothing. There is no exported way to query
`interp.Available()` either, so a caller cannot even detect this gap at
runtime - it can only be known from having read this.

The one escape hatch: build the workspace programmatically instead of via a
magusfile, using the exported `Option`/`ProjectOption`/`BindingOption` wire
API (`register.go`: `WithRegisteredSpell`, `WithTarget`, `WithClaim`, ...).
Built-in spells (`go`, `ts`, `rust`, ...) decode from embedded bytecode
through `internal/spellruntime`, which IS reachable this way because the exported
`register.go` functions live inside this module and call into it on the
caller's behalf - the caller never imports `internal/spellruntime` directly, they
call the exported wrapper. This path gets a caller real spell execution
without a magusfile; it does not get them arbitrary Buzz-authored targets.

## Audit mode

When asked "does the SDK actually give me X", verify, do not infer from
naming:

1. **Is the type exported?** `grep -n "^type <Name>" types/*.go *.go` (or the
   package the docs claim owns it). A type used only inside `internal/` is
   not reachable regardless of what a doc comment implies.
2. **Is the capability reachable without the CLI?** Trace the call from the
   exported entry point (`Open`/`Inspect`/a method on `*Magus`) down through
   its imports. The moment the trace crosses into an `internal/` package that
   nothing exported re-wraps, the capability stops at that package - like the
   Buzz-evaluation gap above.
3. **Is a boundary deliberate or accidental?** Read the package doc comment
   first (`doc.go` or the top of the main file) before proposing a merge -
   `spells/doc.go` documents that `spells` (exported: the Buzz sources plus
   `Op`/`Driver`/`Descriptor`) and `internal/spellruntime` (unexported: the bytecode
   decoder) used to be three packages telling the same story from different
   angles, and were deliberately collapsed to two, with the dependency
   direction (`spells` imports nothing from `types`; `types` imports
   `spells`) called out as load-bearing so the two cannot cycle. That is a
   documented, deliberate split - proposing to merge `internal/spellruntime` into
   `spells` would put an unrelated concern (bytecode framing) behind the
   public API. Contrast with a package that has no doc comment, one importer,
   and nothing it exports that would need to stay exported after a merge -
   that shape (see the magus-architecture-review skill's table) is the accidental
   kind.

Do not paper over a genuine gap with a workaround the reader did not ask for.
If a capability is not reachable, say so plainly and name the exact package
boundary that stops it - the three verified gaps above are the model for how
specific that has to be.
````

## Short form

The enumeration dropped, the judgment kept - for the most capable readers, not the least; the bar under the heading above shows by how much. This is the always-loaded primary. Both are hand-authored from one source body; see [Agents](../../guides/integrations/agents.md) for the difference.

<details>
<summary>Show the short form</summary>

````markdown
# Consuming magus as a Go library

The reader this skill serves has never run `magus` and does not know its
subcommands. They found `github.com/egladman/magus` on pkg.go.dev or in an
import line and want to call it from their own Go program. Ground every
answer in the actual exported surface (`magus.go`, `types/repository.go`,
`describe.go`, `types/describe.go`, `run.go`, `knowledge.go`,
`project/impact/impact.go`) - never in what the CLI does, which this reader
cannot see.

## Before anything else: can they even `go get` it?

VERIFIED against the real published module at the real latest tag
(`go get github.com/egladman/magus@v0.3.0` from a clean module, no local
replace): it fails outright. `go.mod` requires
`github.com/egladman/magus/libs/gopherbuzz` and `.../libs/diagnostics` - nested
modules with their own `go.mod` - at `v0.0.0`, and no `libs/gopherbuzz/vX.Y.Z`
or `libs/diagnostics/vX.Y.Z` tag exists in the repo (only root tags: v0.1.0 - v0.3.0).
The root module's own `go.mod` resolves them via LOCAL replace directives
(`replace github.com/egladman/magus/libs/gopherbuzz => ./libs/gopherbuzz`);
replace directives are not transitive, so a downstream consumer inherits
none of that and hits `unknown revision libs/gopherbuzz/v0.0.0`.

Until those nested modules get their own tags, the only working install is:
clone the repo (or vendor `libs/gopherbuzz` and `libs/diagnostics` from it), and add
matching replace directives to the consumer's own `go.mod`:

```go
require github.com/egladman/magus v0.3.0

replace github.com/egladman/magus/libs/gopherbuzz => /path/to/magus/libs/gopherbuzz
replace github.com/egladman/magus/libs/diagnostics => /path/to/magus/libs/diagnostics
```

Tell the reader this plainly before anything else. A worked example that
compiles for you (inside the module, or via a `replace` on the root) but
silently fails for them with no local checkout is worse than admitting the
gap.

## Entry points

| Call | Returns | Cache | Use when |
|---|---|---|---|
| `magus.Open(ctx, root, opts...)` | `*magus.Magus` | builds one | the caller will `Run` targets or read cached results |
| `magus.Inspect(ctx, root, opts...)` | `types.WorkspaceRepository` | none | pure introspection: list/describe/classify, never execute |

Both discover the workspace root's projects and evaluate magusfiles the same
way (`Open` calls the same `load()` `Inspect` does, then additionally opens
the on-disk cache) - the difference is purely whether a cache gets built, not
what gets discovered.

`magus.FindRoot(dir)` walks up from `dir` (or cwd) to locate the workspace
root before calling either constructor, the same walk the CLI does.

## The interface hierarchy: depend on the narrowest role

`types.WorkspaceRepository` is `WorkspaceReader + TargetExpander +
AffectedComputer + Inspector`, embedded, not one flat interface. `*Magus`
(from `Open`) and the value `Inspect` returns both satisfy the whole thing,
but a function that only reads project facts should take
`types.WorkspaceReader`, not `types.WorkspaceRepository` -
`types/repository.go` says this outright: "Prefer the narrowest embedded role
a consumer actually uses."

| Role | Methods | Answers |
|---|---|---|
| `WorkspaceReader` | `Root`, `All`, `Get`, `Graph`, `VCSOptions`, `Where` | what projects exist, and the project dependency graph |
| `TargetExpander` | `ExpandPath`, `ExpandCwd`, `ExpandAffected` | which concrete `path:target` pairs a target pattern names |
| `AffectedComputer` | `Affected`, `AffectedFromPaths` | which projects a VCS changeset touches |
| `Inspector` | `List*`, `Evaluate*`, `ClassifyFiles`, `TargetGraph`, `Workspace` | see the axis below |

A function that prints project names needs only `WorkspaceReader`; widening
its parameter to `WorkspaceRepository` because that is what `Open` returns is
the over-coupling this hierarchy exists to prevent - it forces every future
caller to construct or stub the whole repository just to satisfy a signature
that only reads `Root()` and `All()`.

## The List / Evaluate / Classify axis

This is the SDK's organizing idea (`types/repository.go`'s `Inspector` doc
comment states it directly): `List*` enumerates a DECLARATION, cheap, no
resolution. `Evaluate*` RESOLVES one - spells bound, claims applied, charms
patched in - and costs more. `ClassifyFiles` and `TargetGraph` are their own
verbs because neither fits the List/Evaluate split cleanly.

| Method | Reads | Cost |
|---|---|---|
| `ListProjects` | declared project facts (project-relative globs) | cheap |
| `ListTargets` | target name -> spell/project vocabulary | cheap |
| `ListCharms` | inverse charm index across every project | most expensive `Inspector` method (renders every charm x target x spell) |
| `EvaluateProjects` | resolved spells, workspace-rooted globs | resolves every project |
| `EvaluateTarget(ctx, t)` | full dispatch plan for one `path:target` | resolves one target |
| `ClassifyFiles(ctx, paths)` | which project owns/declares each path | pure glob lookup, cheap even for a whole dirty tree |
| `TargetGraph(ctx)` | the `ctx.needs` DAG, read statically from magusfile source | never executes a target body |

`ProjectEntry.Sources` (from `ListProjects`) and `EvaluatedProject.Sources`
(from `EvaluateProjects`, via its embedded `ProjectEntry`) are the SAME FIELD
NAME carrying DIFFERENT representations - declared, project-relative globs in
the first case; resolved, workspace-rooted globs (joined against the project
path, magusfile globs folded in) in the second. Reading one where the other
was intended silently mismatches every glob it feeds. This is documented
exactly once, in the field comment on `ProjectEntry.Sources` in
`types/describe.go` - read it before writing code that consumes both.

## ctx and cancellation

Every `Inspector` method takes `ctx` first and returns `error` last,
including the read-only `List*` calls. A cancelled walk returns an error
instead of a truncated slice - `describeCancelled` in `describe.go` names the
walk and how far it got (`"describe projects: cancelled after 2 of 8: context
canceled"`), and wraps `ctx.Err()`, so `errors.Is(err, context.Canceled)` and
`errors.Is(err, context.DeadlineExceeded)` both hold. VERIFIED: cancelling
before calling `ListProjects` on a one-project workspace produces exactly
that message and `errors.Is` returns true.

Never treat an empty or short `List*`/`Evaluate*` result as "the workspace has
nothing" without checking the error first. A partial inventory is
indistinguishable from a small one: a wrong answer wearing a right answer's shape.

## Two different graphs, easy to conflate

- `WorkspaceReader.Graph()` returns `*types.Graph`: the PROJECT dependency
  graph (project -> project, from `depends_on`).
- `Inspector.TargetGraph(ctx)` returns `types.TargetGraphOutput`: the TARGET
  graph (target -> target, from `ctx.needs`), within and across projects,
  read statically from magusfile source - it never runs a target body, so it
  sees both arms of a runtime branch and reports a dependency cycle if one
  exists.

A caller doing scheduling or impact analysis typically holds both: `Graph()`
for "does project A depend on project B", `TargetGraph()` for "does target
`lint` in project A depend on target `build` in project B". Do not reach for
one when the question is about the other's granularity.

## Sharp edges (verified, not folklore)

**`types.Target` is dual-role.** As a work-unit, `Path`/`Name`/`Charms`/`Files`
identify which `path:target` to run. As a policy bag (`Project.TargetPolicies`
values, `EvaluatedTarget.Policy`), only `SkipCache`/`Exclusive`/`Slots`/
`FailOnDrift`/`RetryOnVolatile` are meaningful - the other 6 of its 11 fields
sit unset and must be ignored (see `types/target.go:95-101` for the type's own
disclaimer). A function that receives a `types.Target` needs to know which
role it is playing before reading any field.

**The `Entry`/`Output`/`Report` suffix split needs the ~20-line comment at
the top of `types/describe.go` to make sense**, and most of it is a naming
RULE, not vocabulary: `Entry` is added to a type name only when the bare name
would collide with an existing type (`ProjectEntry` because `types.Project`
exists; `Charm` has no suffix because nothing else claims that name).
`Output` means "the `Inspector` method itself returns this shape"
(`ProjectsOutput`, `TargetGraphOutput`). `Report` means "rebuilt at the render
edge from a plain slice the method actually returned" (`FileReport`,
`CharmReport`) - `ListProjects`/`EvaluateProjects` are the deliberate
exceptions, still returning their `*Output` type directly because they carry
a real `Workspace` field a `{definition, count, items}` envelope cannot
derive. Guess at this pattern instead of reading the comment and you will
misname a type you add.

**Buzz magusfile evaluation is not reachable from outside this module.**
VERIFIED end to end: a separate module importing `github.com/egladman/magus`
(via `replace`, matching the install workaround above), pointed at a
workspace whose only project declaration is a `magusfile.buzz` with an
`export fun build` target, produces `ListTargets` returning ONLY the
hardcoded `ci` anchor and `ListProjects` reporting `resolvedSpells: 0` -
`build` never appears as a runnable target, and no spell is attached. Reason:
magusfile evaluation is gated on `interp.Available()` (`magus.go`'s `load`),
which is only true when `internal/interp/engine/buzz` and
`internal/interp/bindings` are blank-imported - and those are `internal/`
packages under this module, structurally unreachable from any other module's
import graph. Only `cmd/magus` (built inside this repo) links them
(`cmd/magus/packs_interp.go`). `TargetGraph` still works for a Buzz
magusfile in this situation (it reads the source statically via the exported
`libs/gopherbuzz` parser, not the internal engine) - so an external caller
can SEE the target graph a magusfile declares but cannot make magus DISPATCH
it. `forEachSpell` in `magus.go` returns `nil` immediately when a project has
zero resolved spells, so calling `Run` against such a target does not error;
it silently does nothing. There is no exported way to query
`interp.Available()` either, so a caller cannot even detect this gap at
runtime - it can only be known from having read this.

The one escape hatch: build the workspace programmatically instead of via a
magusfile, using the exported `Option`/`ProjectOption`/`BindingOption` wire
API (`register.go`: `WithRegisteredSpell`, `WithTarget`, `WithClaim`, ...).
Built-in spells (`go`, `ts`, `rust`, ...) decode from embedded bytecode
through `internal/spellruntime`, which IS reachable this way because the exported
`register.go` functions live inside this module and call into it on the
caller's behalf - the caller never imports `internal/spellruntime` directly, they
call the exported wrapper. This path gets a caller real spell execution
without a magusfile; it does not get them arbitrary Buzz-authored targets.

## Audit mode

When asked "does the SDK actually give me X", verify, do not infer from
naming:

1. **Is the type exported?** `grep -n "^type <Name>" types/*.go *.go` (or the
   package the docs claim owns it). A type used only inside `internal/` is
   not reachable regardless of what a doc comment implies.
2. **Is the capability reachable without the CLI?** Trace the call from the
   exported entry point (`Open`/`Inspect`/a method on `*Magus`) down through
   its imports. The moment the trace crosses into an `internal/` package that
   nothing exported re-wraps, the capability stops at that package - like the
   Buzz-evaluation gap above.
3. **Is a boundary deliberate or accidental?** Read the package doc comment
   first (`doc.go` or the top of the main file) before proposing a merge -
   `spells/doc.go` documents that `spells` (exported: the Buzz sources plus
   `Op`/`Driver`/`Descriptor`) and `internal/spellruntime` (unexported: the bytecode
   decoder) used to be three packages telling the same story from different
   angles, and were deliberately collapsed to two, with the dependency
   direction (`spells` imports nothing from `types`; `types` imports
   `spells`) called out as load-bearing so the two cannot cycle. That is a
   documented, deliberate split - proposing to merge `internal/spellruntime` into
   `spells` would put an unrelated concern (bytecode framing) behind the
   public API. Contrast with a package that has no doc comment, one importer,
   and nothing it exports that would need to stay exported after a merge -
   that shape (see the magus-architecture-review skill's table) is the accidental
   kind.

Do not paper over a genuine gap with a workaround the reader did not ask for.
If a capability is not reachable, say so plainly and name the exact package
boundary that stops it - the three verified gaps above are the model for how
specific that has to be.
````


</details>
