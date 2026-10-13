---
title: magus-sdk
generated_from: internal/agent/skills/magus-sdk/SKILL.md
description: "Help a Go developer consume magus as a library (import \"github.com/egladman/magus\") instead of shelling out to the CLI, and audit whether the SDK actually serves them."
tags: [agents, skills, magus-sdk]
skill_full_bytes: 14079
skill_short_bytes: 9319
---

# magus-sdk

Help a Go developer consume magus as a library (import "github.com/egladman/magus") instead of shelling out to the CLI, and audit whether the SDK actually serves them. Use when someone wants to call Open/Inspect/Run from their own Go program, embed magus's workspace model in another tool, or asks "can I use magus without the binary". Also use to audit the SDK's exported API itself - whether a type is exported, a concept is reachable without the CLI, and whether a package boundary is deliberate or accidental. Do NOT use for CLI usage (magus-run, magus-query) or for editing magus's own source (magus-architecture-review).

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
| `agent-skill-version` | `122` |
| `knowledge-schema-version` | `17` |
| `skill-content` | `ffbaa31f338c` |
| `skill-variant` | `full` |

The `skill-content` digest covers this skill alone, and both forms below report it: they go stale together, never one silently, and a change to another skill does not move it.

## The two forms

Both are hand-authored from one source body. The short form is the always-loaded primary - the enumeration dropped, the judgment kept, for the most capable readers rather than the least. The full form is its `<name>-full` twin, loaded by name when a reader wants the rationale. The bar above shows how much shorter the primary is; switch between them here to see exactly what it gave up. See [Skills](../../guides/integrations/agents/skills.md) for how to choose.

<article class="landing-tabs">
<header>
<input type="radio" name="magus-sdk-variant" id="magus-sdk-tab-short" checked>
<label for="magus-sdk-tab-short">Short form</label>
<input type="radio" name="magus-sdk-variant" id="magus-sdk-tab-full">
<label for="magus-sdk-tab-full">Full form</label>
</header>

<section class="landing-tabpanel">

```sh
magus agent install --tar | tar -xO -f - magus-sdk/SKILL.md
```

````markdown
# magus as Go library

Reader: never ran `magus`, knows no subcommands; found `github.com/egladman/magus` on pkg.go.dev or import line; wants it in own Go program.
Ground every answer in actual exported API, never in CLI behavior reader cannot see:
- `magus.go`, `run.go`, `knowledge.go`, `describe.go`
- `types/repository.go`, `types/describe.go`
- `project/impact/impact.go`

## Before anything: can they `go get` it?

No. `go get github.com/egladman/magus@v0.3.0` fails outright from clean module.
Root `go.mod` requires nested `libs/gopherbuzz` and `libs/diagnostics` modules at `v0.0.0` through local replace directives.
Both modules carry `v0.1.0` tags root does not require; replaces not transitive.
Until root requires tagged versions, only working install: local copy. Clone repo (or vendor `libs/gopherbuzz` and `libs/diagnostics` from it) -> add matching replace directives to consumer's `go.mod`:

```go
require github.com/egladman/magus v0.3.0

replace github.com/egladman/magus/libs/gopherbuzz => /path/to/magus/libs/gopherbuzz
replace github.com/egladman/magus/libs/diagnostics => /path/to/magus/libs/diagnostics
```

Tell reader plainly, first. Example compiling for you, failing for them with no local checkout: worse than admitting gap.

## Entry points

- `magus.Open(ctx, root, opts...)`: returns `*magus.Magus`; builds cache; use to `Run` targets or read cached results.
- `magus.Inspect(ctx, root, opts...)`: returns `types.WorkspaceRepository`; no cache; pure introspection: list/describe/classify, never execute.

Both discover root's projects, evaluate magusfiles same way.
`Open` calls same `load()` as `Inspect`, then also opens on-disk cache.
Caller who later needs `Run` switches to `Open`; never type-asserts its way there.
`magus.FindRoot(dir)` walks up from `dir` (or cwd) to workspace root, same walk as CLI; call before either constructor.

## Interface hierarchy: depend on narrowest role

`types.WorkspaceRepository` embeds `WorkspaceReader + TargetExpander + AffectedComputer + Inspector`.
`*Magus` (from `Open`) and value `Inspect` returns both satisfy all of it.
Take narrowest role function uses:
- `WorkspaceReader`: `Root`, `All`, `Get`, `Graph`, `VCSOptions`, `Where`; what projects exist, project dependency graph
- `TargetExpander`: `ExpandPath`, `ExpandCwd`, `ExpandAffected`; which concrete `path:target` pairs target pattern names
- `AffectedComputer`: `Affected`, `AffectedFromPaths`; which projects VCS changeset touches
- `Inspector`: `List*`, `Evaluate*`, `ClassifyFiles`, `TargetGraph`, `Workspace`; axis below

Function printing project names needs only `WorkspaceReader`; widening to `WorkspaceRepository` forces every caller to construct or stub whole repository.

## `List` / `Evaluate` / `Classify` axis

Organizing idea:
- `List*`: enumerates DECLARATION; cheap, no resolution.
- `Evaluate*`: RESOLVES one (spells bound, claims applied, charms patched in); costs more.
- `ClassifyFiles`, `TargetGraph`: own verbs; neither fits split.

Method: reads; cost
- `ListProjects`: declared project facts (project-relative globs); cheap
- `ListTargets`: target name -> spell/project vocabulary; cheap
- `ListCharms`: inverse charm index across every project; most expensive `Inspector` method (renders every charm x target x spell)
- `EvaluateProjects`: resolved spells, workspace-rooted globs; resolves every project
- `EvaluateTarget(ctx, t)`: full dispatch plan for one `path:target`; resolves one target
- `ClassifyFiles(ctx, paths)`: which project owns/declares each path; pure glob lookup, cheap even for whole dirty tree
- `TargetGraph(ctx)`: `ctx.needs` DAG, read statically from magusfile source; never executes target body

`ProjectEntry.Sources` and `EvaluatedProject.Sources`: same FIELD NAME, DIFFERENT representations:
- `ListProjects`: declared, project-relative globs.
- `EvaluateProjects` (through embedded `ProjectEntry`): resolved, workspace-rooted globs, joined against project path, magusfile globs folded in.
Reading one where other was meant silently mismatches every glob it feeds.
Field comment on `ProjectEntry.Sources` in `types/describe.go`: only place this is documented; read before writing code consuming both.

## ctx, cancellation

Every `Inspector` method: `ctx` first, `error` last, read-only `List*` calls included.
Cancelled walk returns error, never truncated slice.
- `describeCancelled` in `describe.go` names walk and progress (`"describe projects: cancelled after 2 of 8: context canceled"`).
- Wraps `ctx.Err()` -> `errors.Is(err, context.Canceled)` and `errors.Is(err, context.DeadlineExceeded)` both hold.
Never treat empty or short `List*`/`Evaluate*` result as "the workspace has nothing" without checking error first: partial inventory indistinguishable from small one.

## Two graphs

- `WorkspaceReader.Graph()` returns `*types.Graph`: PROJECT dependency graph (project -> project, from `depends_on`).
- `Inspector.TargetGraph(ctx)` returns `types.TargetGraphOutput`: TARGET graph (target -> target, from `ctx.needs`), within and across projects; read statically, sees both arms of branch, reports cycles.
Match graph to question granularity.

## Sharp edges (verified)

`types.Target` is dual-role; know which before reading any field:
- Work-unit: `Path`/`Name`/`Charms`/`Files` identify which `path:target` to run.
- Policy bag (`Project.TargetPolicies` values, `EvaluatedTarget.Policy`): only `SkipCache`/`Slots`/`Drift`/`RetryOnVolatile` mean anything; ignore other 6 of its 10 fields.

`Entry`/`Output`/`Report` suffix split needs about 20-line comment at top of `types/describe.go`: mostly naming CONVENTION, not vocabulary. Read before adding type.
- `Entry`: added only when bare name would collide with existing type: `ProjectEntry` because `types.Project` exists. `Charm` has no suffix: nothing else claims name.
- `Output`: `Inspector` method itself returns this shape (`ProjectsOutput`, `TargetGraphOutput`).
- `Report`: rebuilt at render edge from plain slice method returned (`FileReport`, `CharmReport`).
- `ListProjects`/`EvaluateProjects`: deliberate exceptions; return `*Output` directly because they carry real `Workspace` field `{definition, count, items}` envelope cannot derive.

Buzz magusfile evaluation not reachable from outside this module:
- `ListTargets` returns ONLY hardcoded `ci` anchor; `ListProjects` reports `resolvedSpells: 0`. Magusfile target never appears runnable; no spell attached.
- `magus.go`'s `load` gates magusfile evaluation on `interp.Available()`: true only when two `internal/` packages blank-imported, which no other module can do.
- `TargetGraph` still works for such magusfile: static read via exported `libs/gopherbuzz` parser, not internal engine. External caller can SEE target graph but cannot make magus DISPATCH it.
- `Run` against such target does not error; silently does nothing.
- Nothing exported queries `interp.Available()` -> caller cannot detect gap at runtime; known only from reading this.

One escape hatch: build workspace programmatically, not through magusfile, via exported `Option`/`ProjectOption`/`BindingOption` wire API (`register.go`: `WithSpell`, `WithTarget`, `WithClaim`, ...).
- Built-in spells (`go`, `ts`, `rust`, ...) decode from embedded bytecode through `internal/spell`; reachable this way.
- Gives real spell execution without magusfile; not arbitrary Buzz-authored targets.

## Testing SDK-using code

Test at narrowest seam code allows; real workspace only for what fake cannot tell you.
- Unit tests: narrowest role (`types.WorkspaceReader`, ...). Fake with mock from `types/gen/mocks`, or embed interface in struct, override one method test calls.
- Integration tests: `testkit.NewWorkspace(t)` (from `libs/testkit`) with `magus.Inspect`; Inspect opens no cache.
- Call `testkit.Main(m)` from `TestMain`, or `testkit.Isolate(t)` in serial test: `Output*` lookups and `PayloadByRef` read session store and other checkouts on miss.
- `Run`: declare one-method interface, fake that; `*magus.Magus` has too many methods to fake whole.
- Magusfile targets do not run outside this module -> test writing one and calling `Run` proves nothing. Register spell from Go with `WithSpell` instead.
- Programmatic spell: `register.Spell(tb, name, opts ...spells.Option)` (`libs/testkit/register`), passing `spells.WithTargets` and similar.

## Audit mode

"does the SDK actually give me X" -> verify; never infer from naming:
1. Type exported? `grep -n "^type <Name>" types/*.go *.go` (or package docs claim owns it). Type used only inside `internal/`: unreachable, whatever doc comment implies.
2. Capability reachable without CLI? Trace call from exported entry point (`Open`/`Inspect`/method on `*Magus`) down through imports. Capability stops where trace crosses into `internal/` package nothing exported re-wraps.
3. Boundary deliberate or accidental? Read package doc comment first (`doc.go` or top of main file) before proposing merge.
   - Deliberate: `spells/doc.go` documents `spells` and `internal/spell` collapsed from three packages to two on purpose.
   - Accidental: package with no doc comment, one importer, nothing exported that must stay exported after merge.

Never paper over genuine gap with workaround reader did not ask for.
If capability not reachable -> say so plainly, name exact package boundary stopping it.
````


</section>

<section class="landing-tabpanel">

```sh
magus agent install --tar | tar -xO -f - magus-sdk-full/SKILL.md
```

````markdown
# Consuming magus as a Go library

This skill's reader has never run `magus` and does not know its subcommands. They
found `github.com/egladman/magus` on pkg.go.dev or in an import line. They want to
call it from their own Go program. Ground every answer in the actual exported API,
never in what the CLI does, which this reader cannot see:

- `magus.go`, `run.go`, `knowledge.go`, `describe.go`
- `types/repository.go`, `types/describe.go`
- `project/impact/impact.go`

## Contents

- Before anything else: can they even `go get` it?
- Entry points
- The interface hierarchy: depend on the narrowest role
- The `List` / `Evaluate` / `Classify` axis
- ctx and cancellation
- Two different graphs, easy to conflate
- Sharp edges (verified, not folklore)
- Testing code that uses the SDK
- Audit mode

## Before anything else: can they even `go get` it?

No. `go get github.com/egladman/magus@v0.3.0` fails outright from a clean module.
VERIFIED against the real published module at the latest tag, with no local replace:

- `go.mod` requires `github.com/egladman/magus/libs/gopherbuzz` and
  `.../libs/diagnostics` (nested modules with their own `go.mod`) at `v0.0.0`.
- Both nested modules carry `v0.1.0` tags (`libs/gopherbuzz/v0.1.0`,
  `libs/diagnostics/v0.1.0`), but the root still requires them at `v0.0.0`.
- The root `go.mod` resolves them through LOCAL replace directives
  (`replace github.com/egladman/magus/libs/gopherbuzz => ./libs/gopherbuzz`).
  Replace directives are not transitive, so a downstream consumer hits `unknown
  revision libs/gopherbuzz/v0.0.0`.

Until the root requires the tagged versions, the only working install is a local
copy. Clone the repo (or vendor `libs/gopherbuzz` and `libs/diagnostics` from it).
Then add matching replace directives to the consumer's `go.mod`:

```go
require github.com/egladman/magus v0.3.0

replace github.com/egladman/magus/libs/gopherbuzz => /path/to/magus/libs/gopherbuzz
replace github.com/egladman/magus/libs/diagnostics => /path/to/magus/libs/diagnostics
```

Tell the reader this plainly, first. A worked example that compiles for you but fails
for them with no local checkout is worse than admitting the gap. Yours
compiles inside the module, or through a `replace` on the root.

## Entry points

| Call | Returns | Cache | Use when |
|---|---|---|---|
| `magus.Open(ctx, root, opts...)` | `*magus.Magus` | builds one | the caller will `Run` targets or read cached results |
| `magus.Inspect(ctx, root, opts...)` | `types.WorkspaceRepository` | none | pure introspection: list/describe/classify, never execute |

Both discover the root's projects and evaluate magusfiles the same way. `Open` calls
the same `load()` as `Inspect`, then also opens the on-disk cache. The only
difference is whether a cache is built, not what is discovered. `Inspect` returning the narrow
`types.WorkspaceRepository` interface rather than the concrete `*Magus` is
itself a hint: an introspection-only caller should code against the
interface, and a caller who later needs `Run` should switch to `Open` and get
the concrete type, not type-assert their way there.

`magus.FindRoot(dir)` walks up from `dir` (or cwd) to the workspace root, the same
walk the CLI does. Call it before either constructor.

## The interface hierarchy: depend on the narrowest role

`types.WorkspaceRepository` embeds `WorkspaceReader + TargetExpander +
AffectedComputer + Inspector`. `*Magus` (from `Open`) and the value `Inspect` returns
both satisfy all of it. Take the narrowest role a function uses.
`types/repository.go` says so:
"Prefer the narrowest embedded role a consumer actually uses."

| Role | Methods | Answers |
|---|---|---|
| `WorkspaceReader` | `Root`, `All`, `Get`, `Graph`, `VCSOptions`, `Where` | what projects exist, and the project dependency graph |
| `TargetExpander` | `ExpandPath`, `ExpandCwd`, `ExpandAffected` | which concrete `path:target` pairs a target pattern names |
| `AffectedComputer` | `Affected`, `AffectedFromPaths` | which projects a VCS changeset touches |
| `Inspector` | `List*`, `Evaluate*`, `ClassifyFiles`, `TargetGraph`, `Workspace` | see the axis below |

A function that prints project names needs only `WorkspaceReader`. Widening its
parameter to `WorkspaceRepository` because `Open` returns that is the over-coupling
this hierarchy prevents: every future caller must construct or stub the whole
repository for a signature that only reads `Root()` and `All()`.

## The `List` / `Evaluate` / `Classify` axis

This is the SDK's organizing idea, stated in the `Inspector` doc comment in
`types/repository.go`:

- `List*` enumerates a DECLARATION: cheap, no resolution.
- `Evaluate*` RESOLVES one (spells bound, claims applied, charms patched in) and
  costs more.
- `ClassifyFiles` and `TargetGraph` are their own verbs; neither fits the split.

| Method | Reads | Cost |
|---|---|---|
| `ListProjects` | declared project facts (project-relative globs) | cheap |
| `ListTargets` | target name -> spell/project vocabulary | cheap |
| `ListCharms` | inverse charm index across every project | most expensive `Inspector` method (renders every charm x target x spell) |
| `EvaluateProjects` | resolved spells, workspace-rooted globs | resolves every project |
| `EvaluateTarget(ctx, t)` | full dispatch plan for one `path:target` | resolves one target |
| `ClassifyFiles(ctx, paths)` | which project owns/declares each path | pure glob lookup, cheap even for a whole dirty tree |
| `TargetGraph(ctx)` | the `ctx.needs` DAG, read statically from magusfile source | never executes a target body |

`ProjectEntry.Sources` and `EvaluatedProject.Sources` share a FIELD NAME with
DIFFERENT representations:

- `ListProjects`: declared, project-relative globs.
- `EvaluateProjects` (through its embedded `ProjectEntry`): resolved,
  workspace-rooted globs, joined against the project path with magusfile globs
  folded in.

Reading one where the other was meant silently mismatches every glob it feeds. The
field comment on `ProjectEntry.Sources` in `types/describe.go` is the one place this
is documented. Read it before writing code that consumes both.

## ctx and cancellation

Every `Inspector` method takes `ctx` first and returns `error` last, the read-only
`List*` calls included. A cancelled walk returns an error, never a truncated slice.

- `describeCancelled` in `describe.go` names the walk and how far it got
  (`"describe projects: cancelled after 2 of 8: context canceled"`).
- It wraps `ctx.Err()`, so `errors.Is(err, context.Canceled)` and
  `errors.Is(err, context.DeadlineExceeded)` both hold.
- VERIFIED: cancelling before `ListProjects` on a one-project workspace produces
  exactly that message, and `errors.Is` returns true.

Never treat an empty or short `List*`/`Evaluate*` result as "the workspace has
nothing" without checking the error first. The reason is stated directly in
describe.go: a partial inventory reporting `Count: 3` is indistinguishable from a
workspace that genuinely has three projects, so silently truncating would be a
wrong answer wearing a right answer's shape.

## Two different graphs, easy to conflate

- `WorkspaceReader.Graph()` returns `*types.Graph`: the PROJECT dependency graph
  (project -> project, from `depends_on`).
- `Inspector.TargetGraph(ctx)` returns `types.TargetGraphOutput`: the TARGET graph
  (target -> target, from `ctx.needs`), within and across projects. It is read
  statically from magusfile source and never runs a target body. It sees both arms
  of a runtime branch and reports a dependency cycle if one exists.

Match the graph to the question's granularity. A caller doing scheduling or impact analysis usually holds both: `Graph()` for "does
project A depend on project B", `TargetGraph()` for "does target `lint` in project A
depend on target `build` in project B".

## Sharp edges (verified, not folklore)

**`types.Target` is dual-role.** Know which role it plays before reading any field:

- As a work-unit, `Path`/`Name`/`Charms`/`Files` identify which `path:target` to run.
- As a policy bag (`Project.TargetPolicies` values, `EvaluatedTarget.Policy`), only
  `SkipCache`/`Slots`/`Drift`/`RetryOnVolatile` mean anything. Ignore the other 6 of
  its 10 fields; they sit unset (see the type's own disclaimer at
  `types/target.go:95-101`).

**The `Entry`/`Output`/`Report` suffix split needs the ~20-line comment at the top
of `types/describe.go`.** Most of it is a naming CONVENTION, not vocabulary. Read the comment
before you add a type; guess at the pattern instead and you misname it.

- `Entry` is added only when the bare name would collide with an existing type:
  `ProjectEntry` because `types.Project` exists. `Charm` has no suffix because
  nothing else claims that name.
- `Output` means the `Inspector` method itself returns this shape
  (`ProjectsOutput`, `TargetGraphOutput`).
- `Report` means rebuilt at the render edge from a plain slice the method returned
  (`FileReport`, `CharmReport`).
- `ListProjects`/`EvaluateProjects` are the deliberate exceptions. They still return
  their `*Output` type directly, because they carry a real `Workspace` field a
  `{definition, count, items}` envelope cannot derive.

**Buzz magusfile evaluation is not reachable from outside this module.** VERIFIED
end to end with a separate module importing `github.com/egladman/magus` (through
`replace`, as in the install workaround). Its workspace's only project declaration
was a `magusfile.buzz` with an `export fun build` target:

- `ListTargets` returns ONLY the hardcoded `ci` anchor, and `ListProjects`
  reports `resolvedSpells: 0`. A magusfile target never appears as runnable, and no
  spell is attached.
- `magus.go`'s `load` gates magusfile evaluation on `interp.Available()`. That is true
  only when two `internal/` packages are blank-imported, which no other module can
  do. They are `internal/interp/engine/buzz` and `internal/interp/bindings`;
  only `cmd/magus` links them (`cmd/magus/packs_interp.go`).
- `TargetGraph` still works for such a magusfile. It reads the source statically
  through the exported `libs/gopherbuzz` parser, not the internal engine. An
  external caller can SEE the target graph but cannot make magus DISPATCH it.
- `Run` against such a target does not error. It silently does nothing:
  `forEachSpell` in `magus.go` returns `nil` at once for a project with zero resolved
  spells.
- Nothing exported queries `interp.Available()`, so a caller cannot detect this gap
  at runtime. It is known only from having read this.

The one escape hatch: build the workspace programmatically, not through a magusfile.
Use the exported `Option`/`ProjectOption`/`BindingOption` wire API (`register.go`:
`WithSpell`, `WithTarget`, `WithClaim`, ...).

- Built-in spells (`go`, `ts`, `rust`, ...) decode from embedded bytecode through
  `internal/spell`. That IS reachable this way: the exported `register.go` functions
  live inside this module and call into it on the caller's behalf. The caller never
  imports `internal/spell`.
- This path gives real spell execution without a magusfile. It does not give
  arbitrary Buzz-authored targets.

## Testing code that uses the SDK

Test at the narrowest seam the code allows. Move to a real workspace only for what a
fake cannot tell you.

- Unit tests take the narrowest role (`types.WorkspaceReader` and so on). Fake it with a
  mock from `types/gen/mocks`, or embed the interface in a struct and override the one
  method the test calls.
- Integration tests use `testkit.NewWorkspace(t)` (from `libs/testkit`) with
  `magus.Inspect`. Inspect opens no cache.
- Call `testkit.Main(m)` from `TestMain`, or `testkit.Isolate(t)` in a serial test. The
  `Output*` lookups and `PayloadByRef` read the session store and other checkouts on a
  miss, so without isolation the result depends on the developer's machine.
- For `Run`, declare a one-method interface and fake that. `*magus.Magus` has too many
  methods to fake whole.
- Magusfile targets do not run outside this module, so a test that writes one and calls
  `Run` proves nothing. Register a spell from Go with `WithSpell` instead.
- Register a programmatic spell with `register.Spell(tb, name, opts ...spells.Option)`
  (`libs/testkit/register`), passing `spells.WithTargets` and the like. The spell registry is process-global, so parallel
  tests that register spells race.

## Audit mode

Asked "does the SDK actually give me X", verify; never infer from naming:

1. **Is the type exported?** `grep -n "^type <Name>" types/*.go *.go` (or the package
   the docs claim owns it). A type used only inside `internal/` is unreachable,
   whatever a doc comment implies.
2. **Is the capability reachable without the CLI?** Trace the call from the exported
   entry point (`Open`/`Inspect`/a method on `*Magus`) down through its imports. The
   capability stops where the trace crosses into an `internal/` package nothing
   exported re-wraps, like the Buzz-evaluation gap above.
3. **Is a boundary deliberate or accidental?** Read the package doc comment first
   (`doc.go` or the top of the main file) before proposing a merge.
   - Deliberate: `spells/doc.go` documents that `spells` and `internal/spell` were
     collapsed from three packages to two on purpose. `spells` is exported: the Buzz sources
     plus `Op`/`Driver`/`Descriptor`; `internal/spell` is the unexported bytecode
     decoder. It calls the
     dependency direction load-bearing (`spells` imports nothing from `types`;
     `types` imports `spells`) so the two cannot cycle. Merging `internal/spell`
     into `spells` would put bytecode framing behind the public API.
   - Accidental: a package with no doc comment, one importer, and nothing it exports
     that must stay exported after a merge. That shape (see the
     magus-architecture-review skill's table) is the accidental kind.

Do not paper over a genuine gap with a workaround the reader did not ask for. If a
capability is not reachable, say so plainly and name the exact package boundary
that stops it. The three verified gaps above show how specific that must be.
````


</section>

</article>
