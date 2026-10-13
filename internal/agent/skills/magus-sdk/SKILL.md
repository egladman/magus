{{if .Full}}# Consuming magus as a Go library{{else}}# magus as Go library{{end}}

{{if .Full}}This skill's reader has never run `magus` and does not know its subcommands. They
found `github.com/egladman/magus` on pkg.go.dev or in an import line. They want to
call it from their own Go program. Ground every answer in the actual exported API,
never in what the CLI does, which this reader cannot see:
{{else}}Reader: never ran `magus`, knows no subcommands; found `github.com/egladman/magus` on pkg.go.dev or import line; wants it in own Go program.
Ground every answer in actual exported API, never in CLI behavior reader cannot see:{{end}}
- `magus.go`, `run.go`, `knowledge.go`, `describe.go`
- `types/repository.go`, `types/describe.go`
- `project/impact/impact.go`

{{if .Full}}## Before anything else: can they even `go get` it?{{else}}## Before anything: can they `go get` it?{{end}}
{{if .Full}}
No. `go get github.com/egladman/magus@v0.3.0` fails outright from a clean module.
VERIFIED against the real published module at the latest tag, with no local replace:
{{end}}
{{if .Full}}- `go.mod` requires `github.com/egladman/magus/libs/gopherbuzz` and
  `.../libs/diagnostics` (nested modules with their own `go.mod`) at `v0.0.0`.
- Both nested modules carry `v0.1.0` tags (`libs/gopherbuzz/v0.1.0`,
  `libs/diagnostics/v0.1.0`), but the root still requires them at `v0.0.0`.
- The root `go.mod` resolves them through LOCAL replace directives
  (`replace github.com/egladman/magus/libs/gopherbuzz => ./libs/gopherbuzz`).
  Replace directives are not transitive, so a downstream consumer hits `unknown
  revision libs/gopherbuzz/v0.0.0`.

Until the root requires the tagged versions, the only working install is a local
copy. Clone the repo (or vendor `libs/gopherbuzz` and `libs/diagnostics` from it).
Then add matching replace directives to the consumer's `go.mod`:{{else}}No. `go get github.com/egladman/magus@v0.3.0` fails outright from clean module.
Root `go.mod` requires nested `libs/gopherbuzz` and `libs/diagnostics` modules at `v0.0.0` through local replace directives.
Both modules carry `v0.1.0` tags root does not require; replaces not transitive.
Until root requires tagged versions, only working install: local copy. Clone repo (or vendor `libs/gopherbuzz` and `libs/diagnostics` from it) -> add matching replace directives to consumer's `go.mod`:{{end}}

```go
require github.com/egladman/magus v0.3.0

replace github.com/egladman/magus/libs/gopherbuzz => /path/to/magus/libs/gopherbuzz
replace github.com/egladman/magus/libs/diagnostics => /path/to/magus/libs/diagnostics
```

{{if .Full}}Tell the reader this plainly, first. A worked example that compiles for you but fails
for them with no local checkout is worse than admitting the gap. Yours
compiles inside the module, or through a `replace` on the root.{{else}}Tell reader plainly, first. Example compiling for you, failing for them with no local checkout: worse than admitting gap.{{end}}

## Entry points

{{if .Full}}| Call | Returns | Cache | Use when |
|---|---|---|---|
| `magus.Open(ctx, root, opts...)` | `*magus.Magus` | builds one | the caller will `Run` targets or read cached results |
| `magus.Inspect(ctx, root, opts...)` | `types.WorkspaceRepository` | none | pure introspection: list/describe/classify, never execute |{{else}}- `magus.Open(ctx, root, opts...)`: returns `*magus.Magus`; builds cache; use to `Run` targets or read cached results.
- `magus.Inspect(ctx, root, opts...)`: returns `types.WorkspaceRepository`; no cache; pure introspection: list/describe/classify, never execute.{{end}}

{{if .Full}}Both discover the root's projects and evaluate magusfiles the same way. `Open` calls
the same `load()` as `Inspect`, then also opens the on-disk cache. The only
difference is whether a cache is built, not what is discovered. `Inspect` returning the narrow
`types.WorkspaceRepository` interface rather than the concrete `*Magus` is
itself a hint: an introspection-only caller should code against the
interface, and a caller who later needs `Run` should switch to `Open` and get
the concrete type, not type-assert their way there.

`magus.FindRoot(dir)` walks up from `dir` (or cwd) to the workspace root, the same
walk the CLI does. Call it before either constructor.{{else}}Both discover root's projects, evaluate magusfiles same way.
`Open` calls same `load()` as `Inspect`, then also opens on-disk cache.
Caller who later needs `Run` switches to `Open`; never type-asserts its way there.
`magus.FindRoot(dir)` walks up from `dir` (or cwd) to workspace root, same walk as CLI; call before either constructor.{{end}}

{{if .Full}}## The interface hierarchy: depend on the narrowest role{{else}}## Interface hierarchy: depend on narrowest role{{end}}

{{if .Full}}`types.WorkspaceRepository` embeds `WorkspaceReader + TargetExpander +
AffectedComputer + Inspector`. `*Magus` (from `Open`) and the value `Inspect` returns
both satisfy all of it. Take the narrowest role a function uses.
`types/repository.go` says so:
"Prefer the narrowest embedded role a consumer actually uses."

| Role | Methods | Answers |
|---|---|---|
| `WorkspaceReader` | `Root`, `All`, `Get`, `Graph`, `VCSOptions`, `Where` | what projects exist, and the project dependency graph |
| `TargetExpander` | `ExpandPath`, `ExpandCwd`, `ExpandAffected` | which concrete `path:target` pairs a target pattern names |
| `AffectedComputer` | `Affected`, `AffectedFromPaths` | which projects a VCS changeset touches |
| `Inspector` | `List*`, `Evaluate*`, `ClassifyFiles`, `TargetGraph`, `Workspace` | see the axis below |{{else}}`types.WorkspaceRepository` embeds `WorkspaceReader + TargetExpander + AffectedComputer + Inspector`.
`*Magus` (from `Open`) and value `Inspect` returns both satisfy all of it.
Take narrowest role function uses:
- `WorkspaceReader`: `Root`, `All`, `Get`, `Graph`, `VCSOptions`, `Where`; what projects exist, project dependency graph
- `TargetExpander`: `ExpandPath`, `ExpandCwd`, `ExpandAffected`; which concrete `path:target` pairs target pattern names
- `AffectedComputer`: `Affected`, `AffectedFromPaths`; which projects VCS changeset touches
- `Inspector`: `List*`, `Evaluate*`, `ClassifyFiles`, `TargetGraph`, `Workspace`; axis below{{end}}

{{if .Full}}A function that prints project names needs only `WorkspaceReader`. Widening its
parameter to `WorkspaceRepository` because `Open` returns that is the over-coupling
this hierarchy prevents: every future caller must construct or stub the whole
repository for a signature that only reads `Root()` and `All()`.{{else}}Function printing project names needs only `WorkspaceReader`; widening to `WorkspaceRepository` forces every caller to construct or stub whole repository.{{end}}

{{if .Full}}## The `List` / `Evaluate` / `Classify` axis{{else}}## `List` / `Evaluate` / `Classify` axis{{end}}
{{if .Full}}
This is the SDK's organizing idea, stated in the `Inspector` doc comment in
`types/repository.go`:
{{end}}
{{if .Full}}- `List*` enumerates a DECLARATION: cheap, no resolution.{{else}}Organizing idea:
- `List*`: enumerates DECLARATION; cheap, no resolution.{{end}}
{{if .Full}}- `Evaluate*` RESOLVES one (spells bound, claims applied, charms patched in) and
  costs more.{{else}}- `Evaluate*`: RESOLVES one (spells bound, claims applied, charms patched in); costs more.{{end}}
{{if .Full}}- `ClassifyFiles` and `TargetGraph` are their own verbs; neither fits the split.{{else}}- `ClassifyFiles`, `TargetGraph`: own verbs; neither fits split.{{end}}

{{if .Full}}| Method | Reads | Cost |
|---|---|---|
| `ListProjects` | declared project facts (project-relative globs) | cheap |
| `ListTargets` | target name -> spell/project vocabulary | cheap |
| `ListCharms` | inverse charm index across every project | most expensive `Inspector` method (renders every charm x target x spell) |
| `EvaluateProjects` | resolved spells, workspace-rooted globs | resolves every project |
| `EvaluateTarget(ctx, t)` | full dispatch plan for one `path:target` | resolves one target |
| `ClassifyFiles(ctx, paths)` | which project owns/declares each path | pure glob lookup, cheap even for a whole dirty tree |
| `TargetGraph(ctx)` | the `ctx.needs` DAG, read statically from magusfile source | never executes a target body |{{else}}Method: reads; cost
- `ListProjects`: declared project facts (project-relative globs); cheap
- `ListTargets`: target name -> spell/project vocabulary; cheap
- `ListCharms`: inverse charm index across every project; most expensive `Inspector` method (renders every charm x target x spell)
- `EvaluateProjects`: resolved spells, workspace-rooted globs; resolves every project
- `EvaluateTarget(ctx, t)`: full dispatch plan for one `path:target`; resolves one target
- `ClassifyFiles(ctx, paths)`: which project owns/declares each path; pure glob lookup, cheap even for whole dirty tree
- `TargetGraph(ctx)`: `ctx.needs` DAG, read statically from magusfile source; never executes target body{{end}}

{{if .Full}}`ProjectEntry.Sources` and `EvaluatedProject.Sources` share a FIELD NAME with
DIFFERENT representations:
{{else}}`ProjectEntry.Sources` and `EvaluatedProject.Sources`: same FIELD NAME, DIFFERENT representations:{{end}}
- `ListProjects`: declared, project-relative globs.
{{if .Full}}- `EvaluateProjects` (through its embedded `ProjectEntry`): resolved,
  workspace-rooted globs, joined against the project path with magusfile globs
  folded in.

Reading one where the other was meant silently mismatches every glob it feeds. The
field comment on `ProjectEntry.Sources` in `types/describe.go` is the one place this
is documented. Read it before writing code that consumes both.{{else}}- `EvaluateProjects` (through embedded `ProjectEntry`): resolved, workspace-rooted globs, joined against project path, magusfile globs folded in.
Reading one where other was meant silently mismatches every glob it feeds.
Field comment on `ProjectEntry.Sources` in `types/describe.go`: only place this is documented; read before writing code consuming both.{{end}}

{{if .Full}}## ctx and cancellation{{else}}## ctx, cancellation{{end}}

{{if .Full}}Every `Inspector` method takes `ctx` first and returns `error` last, the read-only
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
wrong answer wearing a right answer's shape.{{else}}Every `Inspector` method: `ctx` first, `error` last, read-only `List*` calls included.
Cancelled walk returns error, never truncated slice.
- `describeCancelled` in `describe.go` names walk and progress (`"describe projects: cancelled after 2 of 8: context canceled"`).
- Wraps `ctx.Err()` -> `errors.Is(err, context.Canceled)` and `errors.Is(err, context.DeadlineExceeded)` both hold.
Never treat empty or short `List*`/`Evaluate*` result as "the workspace has nothing" without checking error first: partial inventory indistinguishable from small one.{{end}}

{{if .Full}}## Two different graphs, easy to conflate{{else}}## Two graphs{{end}}

{{if .Full}}- `WorkspaceReader.Graph()` returns `*types.Graph`: the PROJECT dependency graph
  (project -> project, from `depends_on`).{{else}}- `WorkspaceReader.Graph()` returns `*types.Graph`: PROJECT dependency graph (project -> project, from `depends_on`).{{end}}
{{if .Full}}- `Inspector.TargetGraph(ctx)` returns `types.TargetGraphOutput`: the TARGET graph
  (target -> target, from `ctx.needs`), within and across projects. It is read
  statically from magusfile source and never runs a target body. It sees both arms
  of a runtime branch and reports a dependency cycle if one exists.{{else}}- `Inspector.TargetGraph(ctx)` returns `types.TargetGraphOutput`: TARGET graph (target -> target, from `ctx.needs`), within and across projects; read statically, sees both arms of branch, reports cycles.
Match graph to question granularity.{{end}}{{if .Full}}

Match the graph to the question's granularity. A caller doing scheduling or impact analysis usually holds both: `Graph()` for "does
project A depend on project B", `TargetGraph()` for "does target `lint` in project A
depend on target `build` in project B".{{end}}

{{if .Full}}## Sharp edges (verified, not folklore){{else}}## Sharp edges (verified){{end}}

{{if .Full}}**`types.Target` is dual-role.** Know which role it plays before reading any field:
{{else}}`types.Target` is dual-role; know which before reading any field:{{end}}
{{if .Full}}- As a work-unit, `Path`/`Name`/`Charms`/`Files` identify which `path:target` to run.{{else}}- Work-unit: `Path`/`Name`/`Charms`/`Files` identify which `path:target` to run.{{end}}
{{if .Full}}- As a policy bag (`Project.TargetPolicies` values, `EvaluatedTarget.Policy`), only
  `SkipCache`/`Slots`/`Drift`/`RetryOnVolatile` mean anything. Ignore the other 6 of
  its 10 fields; they sit unset (see the type's own disclaimer at
  `types/target.go:95-101`).{{else}}- Policy bag (`Project.TargetPolicies` values, `EvaluatedTarget.Policy`): only `SkipCache`/`Slots`/`Drift`/`RetryOnVolatile` mean anything; ignore other 6 of its 10 fields.{{end}}

{{if .Full}}**The `Entry`/`Output`/`Report` suffix split needs the ~20-line comment at the top
of `types/describe.go`.** Most of it is a naming CONVENTION, not vocabulary. Read the comment
before you add a type; guess at the pattern instead and you misname it.
{{else}}`Entry`/`Output`/`Report` suffix split needs about 20-line comment at top of `types/describe.go`: mostly naming CONVENTION, not vocabulary. Read before adding type.{{end}}
{{if .Full}}- `Entry` is added only when the bare name would collide with an existing type:
  `ProjectEntry` because `types.Project` exists. `Charm` has no suffix because
  nothing else claims that name.{{else}}- `Entry`: added only when bare name would collide with existing type: `ProjectEntry` because `types.Project` exists. `Charm` has no suffix: nothing else claims name.{{end}}
{{if .Full}}- `Output` means the `Inspector` method itself returns this shape
  (`ProjectsOutput`, `TargetGraphOutput`).{{else}}- `Output`: `Inspector` method itself returns this shape (`ProjectsOutput`, `TargetGraphOutput`).{{end}}
{{if .Full}}- `Report` means rebuilt at the render edge from a plain slice the method returned
  (`FileReport`, `CharmReport`).{{else}}- `Report`: rebuilt at render edge from plain slice method returned (`FileReport`, `CharmReport`).{{end}}
{{if .Full}}- `ListProjects`/`EvaluateProjects` are the deliberate exceptions. They still return
  their `*Output` type directly, because they carry a real `Workspace` field a
  `{definition, count, items}` envelope cannot derive.{{else}}- `ListProjects`/`EvaluateProjects`: deliberate exceptions; return `*Output` directly because they carry real `Workspace` field `{definition, count, items}` envelope cannot derive.{{end}}{{if .Full}}

**Buzz magusfile evaluation is not reachable from outside this module.** VERIFIED
end to end with a separate module importing `github.com/egladman/magus` (through
`replace`, as in the install workaround). Its workspace's only project declaration
was a `magusfile.buzz` with an `export fun build` target:{{end}}

{{if .Full}}- `ListTargets` returns ONLY the hardcoded `ci` anchor, and `ListProjects`
  reports `resolvedSpells: 0`. A magusfile target never appears as runnable, and no
  spell is attached.{{else}}Buzz magusfile evaluation not reachable from outside this module:
- `ListTargets` returns ONLY hardcoded `ci` anchor; `ListProjects` reports `resolvedSpells: 0`. Magusfile target never appears runnable; no spell attached.{{end}}
{{if .Full}}- `magus.go`'s `load` gates magusfile evaluation on `interp.Available()`. That is true
  only when two `internal/` packages are blank-imported, which no other module can
  do. They are `internal/interp/engine/buzz` and `internal/interp/bindings`;
  only `cmd/magus` links them (`cmd/magus/packs_interp.go`).{{else}}- `magus.go`'s `load` gates magusfile evaluation on `interp.Available()`: true only when two `internal/` packages blank-imported, which no other module can do.{{end}}
{{if .Full}}- `TargetGraph` still works for such a magusfile. It reads the source statically
  through the exported `libs/gopherbuzz` parser, not the internal engine. An
  external caller can SEE the target graph but cannot make magus DISPATCH it.{{else}}- `TargetGraph` still works for such magusfile: static read via exported `libs/gopherbuzz` parser, not internal engine. External caller can SEE target graph but cannot make magus DISPATCH it.{{end}}
{{if .Full}}- `Run` against such a target does not error. It silently does nothing:
  `forEachSpell` in `magus.go` returns `nil` at once for a project with zero resolved
  spells.{{else}}- `Run` against such target does not error; silently does nothing.{{end}}
{{if .Full}}- Nothing exported queries `interp.Available()`, so a caller cannot detect this gap
  at runtime. It is known only from having read this.{{else}}- Nothing exported queries `interp.Available()` -> caller cannot detect gap at runtime; known only from reading this.{{end}}

{{if .Full}}The one escape hatch: build the workspace programmatically, not through a magusfile.
Use the exported `Option`/`ProjectOption`/`BindingOption` wire API (`register.go`:
`WithSpell`, `WithTarget`, `WithClaim`, ...).
{{else}}One escape hatch: build workspace programmatically, not through magusfile, via exported `Option`/`ProjectOption`/`BindingOption` wire API (`register.go`: `WithSpell`, `WithTarget`, `WithClaim`, ...).{{end}}
{{if .Full}}- Built-in spells (`go`, `ts`, `rust`, ...) decode from embedded bytecode through
  `internal/spell`. That IS reachable this way: the exported `register.go` functions
  live inside this module and call into it on the caller's behalf. The caller never
  imports `internal/spell`.{{else}}- Built-in spells (`go`, `ts`, `rust`, ...) decode from embedded bytecode through `internal/spell`; reachable this way.{{end}}
{{if .Full}}- This path gives real spell execution without a magusfile. It does not give
  arbitrary Buzz-authored targets.{{else}}- Gives real spell execution without magusfile; not arbitrary Buzz-authored targets.{{end}}

{{if .Full}}## Testing code that uses the SDK{{else}}## Testing SDK-using code{{end}}

{{if .Full}}Test at the narrowest seam the code allows. Move to a real workspace only for what a
fake cannot tell you.
{{else}}Test at narrowest seam code allows; real workspace only for what fake cannot tell you.{{end}}
{{if .Full}}- Unit tests take the narrowest role (`types.WorkspaceReader` and so on). Fake it with a
  mock from `types/gen/mocks`, or embed the interface in a struct and override the one
  method the test calls.{{else}}- Unit tests: narrowest role (`types.WorkspaceReader`, ...). Fake with mock from `types/gen/mocks`, or embed interface in struct, override one method test calls.{{end}}
{{if .Full}}- Integration tests use `testkit.NewWorkspace(t)` (from `libs/testkit`) with
  `magus.Inspect`. Inspect opens no cache.{{else}}- Integration tests: `testkit.NewWorkspace(t)` (from `libs/testkit`) with `magus.Inspect`; Inspect opens no cache.{{end}}
{{if .Full}}- Call `testkit.Main(m)` from `TestMain`, or `testkit.Isolate(t)` in a serial test. The
  `Output*` lookups and `PayloadByRef` read the session store and other checkouts on a
  miss, so without isolation the result depends on the developer's machine.{{else}}- Call `testkit.Main(m)` from `TestMain`, or `testkit.Isolate(t)` in serial test: `Output*` lookups and `PayloadByRef` read session store and other checkouts on miss.{{end}}
{{if .Full}}- For `Run`, declare a one-method interface and fake that. `*magus.Magus` has too many
  methods to fake whole.{{else}}- `Run`: declare one-method interface, fake that; `*magus.Magus` has too many methods to fake whole.{{end}}
{{if .Full}}- Magusfile targets do not run outside this module, so a test that writes one and calls
  `Run` proves nothing. Register a spell from Go with `WithSpell` instead.{{else}}- Magusfile targets do not run outside this module -> test writing one and calling `Run` proves nothing. Register spell from Go with `WithSpell` instead.{{end}}
{{if .Full}}- Register a programmatic spell with `register.Spell(tb, name, opts ...spells.Option)`
  (`libs/testkit/register`), passing `spells.WithTargets` and the like. The spell registry is process-global, so parallel
  tests that register spells race.{{else}}- Programmatic spell: `register.Spell(tb, name, opts ...spells.Option)` (`libs/testkit/register`), passing `spells.WithTargets` and similar.{{end}}

## Audit mode

{{if .Full}}Asked "does the SDK actually give me X", verify; never infer from naming:
{{else}}"does the SDK actually give me X" -> verify; never infer from naming:{{end}}
{{if .Full}}1. **Is the type exported?** `grep -n "^type <Name>" types/*.go *.go` (or the package
   the docs claim owns it). A type used only inside `internal/` is unreachable,
   whatever a doc comment implies.{{else}}1. Type exported? `grep -n "^type <Name>" types/*.go *.go` (or package docs claim owns it). Type used only inside `internal/`: unreachable, whatever doc comment implies.{{end}}
{{if .Full}}2. **Is the capability reachable without the CLI?** Trace the call from the exported
   entry point (`Open`/`Inspect`/a method on `*Magus`) down through its imports. The
   capability stops where the trace crosses into an `internal/` package nothing
   exported re-wraps, like the Buzz-evaluation gap above.{{else}}2. Capability reachable without CLI? Trace call from exported entry point (`Open`/`Inspect`/method on `*Magus`) down through imports. Capability stops where trace crosses into `internal/` package nothing exported re-wraps.{{end}}
{{if .Full}}3. **Is a boundary deliberate or accidental?** Read the package doc comment first
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
     {{skill "architecture-review"}} skill's table) is the accidental kind.{{else}}3. Boundary deliberate or accidental? Read package doc comment first (`doc.go` or top of main file) before proposing merge.
   - Deliberate: `spells/doc.go` documents `spells` and `internal/spell` collapsed from three packages to two on purpose.
   - Accidental: package with no doc comment, one importer, nothing exported that must stay exported after merge.{{end}}

{{if .Full}}Do not paper over a genuine gap with a workaround the reader did not ask for. If a
capability is not reachable, say so plainly and name the exact package boundary
that stops it. The three verified gaps above show how specific that must be.{{else}}Never paper over genuine gap with workaround reader did not ask for.
If capability not reachable -> say so plainly, name exact package boundary stopping it.{{end}}
