# Consuming magus as a Go library

This skill's reader has never run `magus` and does not know its subcommands. They
found `github.com/egladman/magus` on pkg.go.dev or in an import line and want to call
it from their own Go program. Ground every answer in the actual exported surface,
never in what the CLI does, which this reader cannot see:

- `magus.go`, `run.go`, `knowledge.go`, `describe.go`
- `types/repository.go`, `types/describe.go`
- `project/impact/impact.go`

## Before anything else: can they even `go get` it?

No. VERIFIED against the real published module at the latest tag (`go get
github.com/egladman/magus@v0.3.0` from a clean module, no local replace): it fails
outright.

- `go.mod` requires `github.com/egladman/magus/libs/gopherbuzz` and
  `.../libs/diagnostics` (nested modules with their own `go.mod`) at `v0.0.0`.
- No `libs/gopherbuzz/vX.Y.Z` or `libs/diagnostics/vX.Y.Z` tag exists; only root
  tags (v0.1.0 to v0.3.0).
- The root `go.mod` resolves them through LOCAL replace directives
  (`replace github.com/egladman/magus/libs/gopherbuzz => ./libs/gopherbuzz`).
  Replace directives are not transitive, so a downstream consumer hits `unknown
  revision libs/gopherbuzz/v0.0.0`.

Until those nested modules have their own tags, the only working install: clone the
repo (or vendor `libs/gopherbuzz` and `libs/diagnostics` from it), and add matching
replace directives to the consumer's `go.mod`:

```go
require github.com/egladman/magus v0.3.0

replace github.com/egladman/magus/libs/gopherbuzz => /path/to/magus/libs/gopherbuzz
replace github.com/egladman/magus/libs/diagnostics => /path/to/magus/libs/diagnostics
```

Tell the reader this plainly, first. A worked example that compiles for you (inside
the module, or through a `replace` on the root) but fails for them with no local
checkout is worse than admitting the gap.

## Entry points

| Call | Returns | Cache | Use when |
|---|---|---|---|
| `magus.Open(ctx, root, opts...)` | `*magus.Magus` | builds one | the caller will `Run` targets or read cached results |
| `magus.Inspect(ctx, root, opts...)` | `types.WorkspaceRepository` | none | pure introspection: list/describe/classify, never execute |

Both discover the root's projects and evaluate magusfiles the same way: `Open` calls
the same `load()` as `Inspect`, then also opens the on-disk cache. The only
difference is whether a cache is built, not what is discovered.{{if .Full}} `Inspect` returning the narrow
`types.WorkspaceRepository` interface rather than the concrete `*Magus` is
itself a hint: an introspection-only caller should code against the
interface, and a caller who later needs `Run` should switch to `Open` and get
the concrete type, not type-assert their way there.{{end}}

`magus.FindRoot(dir)` walks up from `dir` (or cwd) to the workspace root, the same
walk the CLI does. Call it before either constructor.

## The interface hierarchy: depend on the narrowest role

`types.WorkspaceRepository` embeds `WorkspaceReader + TargetExpander +
AffectedComputer + Inspector`; it is not one flat interface. `*Magus` (from `Open`)
and the value `Inspect` returns both satisfy all of it. A function that only reads
project facts takes `types.WorkspaceReader`, not `types.WorkspaceRepository`.
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

`ProjectEntry.Sources` (from `ListProjects`) and `EvaluatedProject.Sources` (from
`EvaluateProjects`, through its embedded `ProjectEntry`) are the SAME FIELD NAME with
DIFFERENT representations:

- `ListProjects`: declared, project-relative globs.
- `EvaluateProjects`: resolved, workspace-rooted globs (joined against the project
  path, magusfile globs folded in).

Reading one where the other was meant silently mismatches every glob it feeds. The
field comment on `ProjectEntry.Sources` in `types/describe.go` is the one place this
is documented; read it before writing code that consumes both.

## ctx and cancellation

Every `Inspector` method takes `ctx` first and returns `error` last, the read-only
`List*` calls included. A cancelled walk returns an error, never a truncated slice.

- `describeCancelled` in `describe.go` names the walk and how far it got
  (`"describe projects: cancelled after 2 of 8: context canceled"`).
- It wraps `ctx.Err()`, so `errors.Is(err, context.Canceled)` and
  `errors.Is(err, context.DeadlineExceeded)` both hold.{{if .Full}}
- VERIFIED: cancelling before `ListProjects` on a one-project workspace produces
  exactly that message, and `errors.Is` returns true.{{end}}

Never treat an empty or short `List*`/`Evaluate*` result as "the workspace has
nothing" without checking the error first.{{if .Full}} The reason is stated directly in
describe.go: a partial inventory reporting `Count: 3` is indistinguishable from a
workspace that genuinely has three projects, so silently truncating would be a
wrong answer wearing a right answer's shape.{{else}} A partial inventory is
indistinguishable from a small one: a wrong answer wearing a right answer's shape.{{end}}

## Two different graphs, easy to conflate

- `WorkspaceReader.Graph()` returns `*types.Graph`: the PROJECT dependency graph
  (project -> project, from `depends_on`).
- `Inspector.TargetGraph(ctx)` returns `types.TargetGraphOutput`: the TARGET graph
  (target -> target, from `ctx.needs`), within and across projects. It is read
  statically from magusfile source and never runs a target body. It sees both arms
  of a runtime branch and reports a dependency cycle if one exists.

A caller doing scheduling or impact analysis usually holds both: `Graph()` for "does
project A depend on project B", `TargetGraph()` for "does target `lint` in project A
depend on target `build` in project B". Match the graph to the question's
granularity.

## Sharp edges (verified, not folklore)

**`types.Target` is dual-role.** Know which role it plays before reading any field:

- As a work-unit, `Path`/`Name`/`Charms`/`Files` identify which `path:target` to run.
- As a policy bag (`Project.TargetPolicies` values, `EvaluatedTarget.Policy`), only
  `SkipCache`/`Slots`/`Drift`/`RetryOnVolatile` mean anything. The other 6 of its
  10 fields sit unset and must be ignored (see the type's own disclaimer at
  `types/target.go:95-101`).

**The `Entry`/`Output`/`Report` suffix split needs the ~20-line comment at the top
of `types/describe.go`.** Most of it is a naming RULE, not vocabulary. Guess at the
pattern instead of reading the comment and you misname a type you add.

- `Entry` is added only when the bare name would collide with an existing type:
  `ProjectEntry` because `types.Project` exists; `Charm` has no suffix because
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

- `ListTargets` returned ONLY the hardcoded `ci` anchor, and `ListProjects`
  reported `resolvedSpells: 0`. `build` never appears as a runnable target, and no
  spell is attached.
- Why: `magus.go`'s `load` gates magusfile evaluation on `interp.Available()`, true
  only when two `internal/` packages are blank-imported, which no other module can
  do{{if .Full}}. They are `internal/interp/engine/buzz` and `internal/interp/bindings`;
  only `cmd/magus` links them (`cmd/magus/packs_interp.go`){{end}}.
- `TargetGraph` still works for such a magusfile: it reads the source statically
  through the exported `libs/gopherbuzz` parser, not the internal engine. An
  external caller can SEE the target graph but cannot make magus DISPATCH it.
- `Run` against such a target does not error: `forEachSpell` in `magus.go` returns
  `nil` at once for a project with zero resolved spells, so it silently does
  nothing.
- Nothing exported queries `interp.Available()`, so a caller cannot detect this gap
  at runtime. It is known only from having read this.

The one escape hatch: build the workspace programmatically, not through a magusfile,
with the exported `Option`/`ProjectOption`/`BindingOption` wire API (`register.go`:
`WithRegisteredSpell`, `WithTarget`, `WithClaim`, ...).

- Built-in spells (`go`, `ts`, `rust`, ...) decode from embedded bytecode through
  `internal/spell`. That IS reachable this way: the exported `register.go` functions
  live inside this module and call into it on the caller's behalf. The caller never
  imports `internal/spell`.
- This path gives real spell execution without a magusfile. It does not give
  arbitrary Buzz-authored targets.

## Audit mode

Asked "does the SDK actually give me X", verify; never infer from naming:

1. **Is the type exported?** `grep -n "^type <Name>" types/*.go *.go` (or the package
   the docs claim owns it). A type used only inside `internal/` is unreachable,
   whatever a doc comment implies.
2. **Is the capability reachable without the CLI?** Trace the call from the exported
   entry point (`Open`/`Inspect`/a method on `*Magus`) down through its imports. Where
   the trace crosses into an `internal/` package nothing exported re-wraps, the
   capability stops, like the Buzz-evaluation gap above.
3. **Is a boundary deliberate or accidental?** Read the package doc comment first
   (`doc.go` or the top of the main file) before proposing a merge.
   - Deliberate: `spells/doc.go` documents that `spells` (exported: the Buzz sources
     plus `Op`/`Driver`/`Descriptor`) and `internal/spell` (unexported: the bytecode
     decoder) were collapsed from three packages to two on purpose. It calls the
     dependency direction load-bearing (`spells` imports nothing from `types`;
     `types` imports `spells`) so the two cannot cycle. Merging `internal/spell`
     into `spells` would put bytecode framing behind the public API.
   - Accidental: a package with no doc comment, one importer, and nothing it exports
     that must stay exported after a merge. That shape (see the
     {{skill "architecture-review"}} skill's table) is the accidental kind.

Do not paper over a genuine gap with a workaround the reader did not ask for. If a
capability is not reachable, say so plainly and name the exact package boundary
that stops it. The three verified gaps above show how specific that must be.
