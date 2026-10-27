---
title: magus-sdk
generated_from: internal/agent/skills/magus-sdk/SKILL.md
description: "Help a Go developer consume magus as a library (import \"github.com/egladman/magus\") instead of shelling out to the CLI, and audit whether the SDK actually serves them."
tags: [agents, skills, magus-sdk]
skill_full_bytes: 14014
skill_short_bytes: 10182
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
| `agent-skill-version` | `149` |
| `knowledge-schema-version` | `24` |
| `skill-content` | `4fec34cfe5e8` |
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

```markdown
# Consuming magus as a Go library

The reader has never run `magus`; they found `github.com/egladman/magus` on
pkg.go.dev and want to call it from Go. Ground every answer in the exported API,
never the CLI:

- `magus.go`, `register.go`, `run.go`, `judge.go`, `knowledge.go`, `describe.go`
- `types/repository.go`, `types/describe.go`
- `project/impact/impact.go`

## Before anything else: can they `go get` it?

State what is verified, and claim no more. The root `go.mod` requires the nested
`libs/gopherbuzz` and `libs/diagnostics` modules at `v0.2.0`, and both tags are on
the origin (`git ls-remote --tags origin` lists `libs/gopherbuzz/v0.2.0` and
`libs/diagnostics/v0.2.0`).

Say `go get` succeeds only for a version you ran it against, and say how: a scratch
module, `go get github.com/egladman/magus@<version>`, then `go build ./...`.

## Entry points

| Call | Returns | Cache | Use when |
|---|---|---|---|
| `magus.Open(ctx, root, opts...)` | `*magus.Magus` | builds one | the caller will `Run` targets or read cached results |
| `magus.Inspect(ctx, root, opts...)` | `types.WorkspaceRepository` | none | pure introspection: list/describe/classify, never execute |

A caller who later needs
`Run` switches to `Open`, never type-asserts.

Both require `magus.WithSpawnRule(magus.TierCeiling)` (it has no default), and
a workspace with a magusfile needs `import _ "github.com/egladman/magus/buzzengine"`.
`magus.FindRoot(dir)` walks up from `dir` to the workspace root, the same walk the CLI
does; an empty `dir` is an error, not the working directory.

| Call | What it answers |
|---|---|
| `(*Magus).JudgeCommand(ctx, session, dir, cmd)`, `JudgeWrite(..., path)` | the guard's `types.GuardJudgment`: decision, `Rule`, `Reason`, `Next`; a deny is an answer, the error is a judgment that could not be made |
| `(*Magus).Run(ctx, targets, magus.WithSession(id))` | judges each target first; a deny is a `*types.GuardDeniedError` and nothing runs |
| `WithEnv`, `WithStderr`, `WithCommandLine` | the process state a workspace reads; without each it reads no environment, discards notices, records an empty command line |
| `WithVCSDriver`, `WithCacheBackend` (Open), `WithObserver` (Run) | the three swappable parts, one option each |
| `std.MagusPutJob`, `MagusQuery`, `MagusRefs`, `MagusTrailRead`, ... | the Buzz host modules' Go doors, with typed options (`types.JobPut`, `types.KnowledgeQueryOptions`, ...); `std` is in the Go API lock |

The lease a call acts under rides `ctx` (`types.WithJobLease`). The SDK never retries,
re-grades or routes around a refusal.

## The interface hierarchy: depend on the narrowest role

`types.WorkspaceRepository` embeds `WorkspaceReader + TargetExpander +
AffectedComputer + Inspector`. `*Magus` (from `Open`) and the value `Inspect` returns
both satisfy all of it. Take the narrowest role a function uses

| Role | Methods | Answers |
|---|---|---|
| `WorkspaceReader` | `Root`, `All`, `Get`, `Graph`, `VCSOptions`, `Where` | what projects exist, and the project dependency graph |
| `TargetExpander` | `ExpandPath`, `ExpandAffected` | which concrete `path:target` pairs a target pattern names |
| `AffectedComputer` | `Affected`, `AffectedFromPaths` | which projects a VCS changeset touches |
| `Inspector` | `List*`, `Evaluate*`, `ClassifyFiles`, `TargetGraph`, `Workspace` | see the axis below |

A function that prints project names needs only `WorkspaceReader`. Widening it to
`WorkspaceRepository` makes every caller construct or stub the whole repository.

## The `List` / `Evaluate` / `Classify` axis

This is the SDK's organizing idea:

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

The
field comment on `ProjectEntry.Sources` in `types/describe.go` is the one place this
is documented. Read it before writing code that consumes both.

## ctx and cancellation

Every `Inspector` method takes `ctx` first and returns `error` last, the read-only
`List*` calls included. A cancelled walk returns an error, never a truncated slice.

- The error wraps `ctx.Err()`, so `errors.Is(err, context.Canceled)` and
  `errors.Is(err, context.DeadlineExceeded)` both hold.

Never treat an empty or short `List*`/`Evaluate*` result as "the workspace has
nothing" without checking the error first. A partial inventory is
indistinguishable from a small one: a wrong answer wearing a right answer's shape.

## Two different graphs, easy to conflate

- `WorkspaceReader.Graph()` returns `*types.Graph`: the PROJECT dependency graph
  (project -> project, from `depends_on`).
- `Inspector.TargetGraph(ctx)` returns `types.TargetGraphOutput`: the TARGET graph
  (target -> target, from `ctx.needs`), within and across projects. It is read
  statically, sees both arms of a branch, and reports cycles.

Match the graph to the question's granularity.

## Sharp edges (verified, not folklore)

**`types.Target` is dual-role.** Know which role it plays before reading any field:

- As a work-unit, `Path`/`Name`/`Charms`/`Files` identify which `path:target` to run.
- As a policy bag (`Project.TargetPolicies` values, `EvaluatedTarget.Policy`), only
  `SkipCache`/`Slots`/`Drift`/`RetryOnVolatile` mean anything. Ignore the other 6 of
  its 10 fields.

**The `Entry`/`Output`/`Report` suffix split needs the ~20-line comment at the top
of `types/describe.go`.** Most of it is a naming CONVENTION, not vocabulary. Read the comment
before you add a type.

- `Entry` is added only when the bare name would collide with an existing type:
  `ProjectEntry` because `types.Project` exists. `Charm` has no suffix because
  nothing else claims that name.
- `Output` means the `Inspector` method itself returns this shape
  (`ProjectsOutput`, `TargetGraphOutput`).
- `Report` means rebuilt at the render edge from a plain slice the method returned
  (`FileReport`, `CharmReport`).
- `ListProjects`/`EvaluateProjects` are the deliberate exceptions: they return their
  `*Output` type directly.

**Evaluating a magusfile needs the Buzz engine linked.** A program that loads
magusfiles blank-imports `github.com/egladman/magus/buzzengine`, as `cmd/magus` does.
Without it, `Open` and `Inspect` fail with `*types.EngineNotLinkedError` (MGS1104) once
any project has a magusfile, and never return a workspace missing its targets. A
workspace with no magusfile opens without the engine.

The other path builds the workspace in Go: the exported `Option`/`ProjectOption`/
`BindingOption` wire API (`register.go`: `WithSpell`, `WithTarget`, `WithDependsOn`,
...). A target named with `WithTarget` counts as declared, as an `export fun` does.
Built-in spells (`go`, `ts`, `rust`, ...) are reachable this way.

## Testing code that uses the SDK

Test at the narrowest seam the code allows. Move to a real workspace only for what a
fake cannot tell you.

- Unit tests take the narrowest role (`types.WorkspaceReader` and so on). Fake it by
  embedding the interface in a struct and overriding the one method the test calls. A
  test that asserts on the call generates its own mock: magus's `types` mocks are internal.
- Integration tests use `testkit.NewWorkspace(t)` (from `libs/testkit`) with
  `magus.Inspect`. Inspect opens no cache.
- Call `testkit.Main(m)` from `TestMain`, or `testkit.Isolate(t)` in a serial test. The
  `Output*` lookups and `PayloadByRef` read the session store and other checkouts on a
  miss.
- For `Run`, declare a one-method interface and fake that. `*magus.Magus` has too many
  methods to fake whole.
- Magusfile targets do not run outside this module, so a test that writes one and calls
  `Run` proves nothing. Register a spell from Go with `WithSpell` instead.
- Register a programmatic spell with `register.Spell(tb, name, opts ...spells.Option)`
  (`libs/testkit/register`), passing `spells.WithTargets` and the like.

## Audit mode

Asked "does the SDK actually give me X", verify; never infer from naming:

1. **Is the type exported?** `grep -n "^type <Name>" types/*.go *.go` (or the package
   the docs claim owns it). A type used only inside `internal/` is unreachable,
   whatever a doc comment implies.
2. **Is the capability reachable without the CLI?** Trace the call from the exported
   entry point (`Open`/`Inspect`/a method on `*Magus`) down through its imports. The
   capability stops where the trace crosses into an `internal/` package nothing
   exported re-wraps.
3. **Is a boundary deliberate or accidental?** Read the package doc comment first
   (`doc.go` or the top of the main file) before proposing a merge.
   - Deliberate: `spells/doc.go` documents that `spells` and `internal/spell` were
     collapsed from three packages to two on purpose.
   - Accidental: a package with no doc comment, one importer, and nothing it exports
     that must stay exported after a merge.

Do not paper over a genuine gap with a workaround the reader did not ask for. If a
capability is not reachable, say so plainly and name the exact package boundary
that stops it.
```


</section>

<section class="landing-tabpanel">

```sh
magus agent install --tar | tar -xO -f - magus-sdk-full/SKILL.md
```

```markdown
# Consuming magus as a Go library

This skill's reader has never run `magus` and does not know its subcommands. They
found `github.com/egladman/magus` on pkg.go.dev or in an import line. They want to
call it from their own Go program. Ground every answer in the actual exported API,
never in what the CLI does, which this reader cannot see:

- `magus.go`, `register.go`, `run.go`, `judge.go`, `knowledge.go`, `describe.go`
- `types/repository.go`, `types/describe.go`
- `project/impact/impact.go`

## Contents

- Before anything else: can they `go get` it?
- Entry points
- The interface hierarchy: depend on the narrowest role
- The `List` / `Evaluate` / `Classify` axis
- ctx and cancellation
- Two different graphs, easy to conflate
- Sharp edges (verified, not folklore)
- Testing code that uses the SDK
- Audit mode

## Before anything else: can they `go get` it?

State what is verified, and claim no more. The root `go.mod` requires the nested
`libs/gopherbuzz` and `libs/diagnostics` modules at `v0.2.0`, and both tags are on
the origin (`git ls-remote --tags origin` lists `libs/gopherbuzz/v0.2.0` and
`libs/diagnostics/v0.2.0`). Its `replace` directives point at the working
tree for in-repo builds only; a replace is never transitive, so a consumer resolves the
tagged versions the requires name. An older root tag may still require them at `v0.0.0`
and fail as `unknown revision libs/gopherbuzz/v0.0.0`.

Say `go get` succeeds only for a version you ran it against, and say how: a scratch
module, `go get github.com/egladman/magus@<version>`, then `go build ./...`. A worked
example that compiles for you inside the module but fails for them is worse than
admitting you did not check. `go install .../cmd/magus@<version>` refuses the
root module at any version, since its `go.mod` carries replace directives.

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

Both require `magus.WithSpawnRule(magus.TierCeiling)` (it has no default), and
a workspace with a magusfile needs `import _ "github.com/egladman/magus/buzzengine"`.
`magus.FindRoot(dir)` walks up from `dir` to the workspace root, the same walk the CLI
does; an empty `dir` is an error, not the working directory.

| Call | What it answers |
|---|---|
| `(*Magus).JudgeCommand(ctx, session, dir, cmd)`, `JudgeWrite(..., path)` | the guard's `types.GuardJudgment`: decision, `Rule`, `Reason`, `Next`; a deny is an answer, the error is a judgment that could not be made |
| `(*Magus).Run(ctx, targets, magus.WithSession(id))` | judges each target first; a deny is a `*types.GuardDeniedError` and nothing runs |
| `WithEnv`, `WithStderr`, `WithCommandLine` | the process state a workspace reads; without each it reads no environment, discards notices, records an empty command line |
| `WithVCSDriver`, `WithCacheBackend` (Open), `WithObserver` (Run) | the three swappable parts, one option each |
| `std.MagusPutJob`, `MagusQuery`, `MagusRefs`, `MagusTrailRead`, ... | the Buzz host modules' Go doors, with typed options (`types.JobPut`, `types.KnowledgeQueryOptions`, ...); `std` is in the Go API lock |

The lease a call acts under rides `ctx` (`types.WithJobLease`). The SDK never retries,
re-grades or routes around a refusal. It has no agent loop: it answers what a loop asks
and never decides what runs next.

## The interface hierarchy: depend on the narrowest role

`types.WorkspaceRepository` embeds `WorkspaceReader + TargetExpander +
AffectedComputer + Inspector`. `*Magus` (from `Open`) and the value `Inspect` returns
both satisfy all of it. Take the narrowest role a function uses.
`types/repository.go` says so:
"Prefer the narrowest embedded role a consumer actually uses."

| Role | Methods | Answers |
|---|---|---|
| `WorkspaceReader` | `Root`, `All`, `Get`, `Graph`, `VCSOptions`, `Where` | what projects exist, and the project dependency graph |
| `TargetExpander` | `ExpandPath`, `ExpandAffected` | which concrete `path:target` pairs a target pattern names |
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
- The error wraps `ctx.Err()`, so `errors.Is(err, context.Canceled)` and
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
- `ListProjects`/`EvaluateProjects` are the deliberate exceptions: they return their
  `*Output` type directly, because they carry a real `Workspace` field a
  `{definition, count, items}` envelope cannot derive.

**Evaluating a magusfile needs the Buzz engine linked.** A program that loads
magusfiles blank-imports `github.com/egladman/magus/buzzengine`, as `cmd/magus` does.
Without it, `Open` and `Inspect` fail with `*types.EngineNotLinkedError` (MGS1104) once
any project has a magusfile, and never return a workspace missing its targets. A
workspace with no magusfile opens without the engine.

`TargetGraph` reads magusfile source statically through the exported `libs/gopherbuzz`
parser, with or without the engine.

The other path builds the workspace in Go: the exported `Option`/`ProjectOption`/
`BindingOption` wire API (`register.go`: `WithSpell`, `WithTarget`, `WithDependsOn`,
...). A target named with `WithTarget` counts as declared, as an `export fun` does.
Built-in spells (`go`, `ts`, `rust`, ...) are reachable this way: the
exported `register.go` functions call `internal/spell` on the caller's behalf.

## Testing code that uses the SDK

Test at the narrowest seam the code allows. Move to a real workspace only for what a
fake cannot tell you.

- Unit tests take the narrowest role (`types.WorkspaceReader` and so on). Fake it by
  embedding the interface in a struct and overriding the one method the test calls. A
  test that asserts on the call generates its own mock: magus's `types` mocks are internal.
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
   exported re-wraps. `go-api-generate` fails on an exported signature that
   names an `internal/` type, so the locked packages cannot leak one.
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
that stops it, as specific as the MGS1104 refusal above.
```


</section>

</article>
