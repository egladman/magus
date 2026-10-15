---
title: The Go SDK
description: Use magus as a Go library instead of the CLI (Open vs Inspect, the interface hierarchy, the List/Evaluate/Classify axis, ctx and cancellation, testing code that uses the SDK, and what the SDK does not give you yet).
tags: [go, sdk, library, api, embedding, import, package, reference]
---

# The Go SDK

This page is for a Go developer who wants to call magus from their own
program (`import "github.com/egladman/magus"`) rather than shelling out to
the `magus` binary. If you have never run `magus` and do not care to, this is
the page for you; the [CLI reference](cli.md) describes the same domain from
the other side.

## Before you `go get` it

The root module requires two nested modules, each with its own `go.mod`:
`github.com/egladman/magus/libs/gopherbuzz` and
`github.com/egladman/magus/libs/diagnostics`. The root `go.mod` requires both at
`v0.2.0`, and both tags are on the origin (`libs/gopherbuzz/v0.2.0`,
`libs/diagnostics/v0.2.0`); `git ls-remote --tags origin` lists them. The
`replace` directives in that `go.mod` point at the working tree for builds inside
the repository only. A `replace` is never transitive, so your build resolves the
tagged versions the requires name.

This page does not claim `go get` succeeds at any particular release, because
nobody ran it against one for this page. Check the version you pick in a scratch
module before you depend on it:

```sh
go mod init example.com/try
go get github.com/egladman/magus@<version>
go build ./...
```

An older root tag may still require the nested modules at `v0.0.0`, which fails
as `unknown revision libs/gopherbuzz/v0.0.0`. `go install
github.com/egladman/magus/cmd/magus@<version>` refuses the root module at any
version, since its `go.mod` carries `replace` directives; install the binary from
a release instead.

## `Open` vs `Inspect`

Two constructors discover a workspace and return a handle to it. Both walk
up from a root directory, load `magus.yaml`, discover projects, and evaluate
each project's magusfile the same way; the only difference is whether a
content-addressed cache gets built.

```go
ctx := context.Background()

// Inspect: read-only, no cache. Use this for introspection - listing,
// describing, classifying files - and nothing else.
ws, err := magus.Inspect(ctx, "/path/to/workspace", magus.WithSpawnRule(magus.TierCeiling))

// Open: builds an on-disk cache and returns the concrete *magus.Magus,
// which adds Run. Use this when you intend to execute targets.
m, err := magus.Open(ctx, "/path/to/workspace", magus.WithSpawnRule(magus.TierCeiling))
```

Both require a spawn rule: the rule every job spawn is held to. A workspace has no
default one, so leaving `WithSpawnRule` out fails the constructor, naming
`magus.TierCeiling`, the reference rule the CLI passes.

`Inspect` returns `types.WorkspaceRepository`, an interface: a signal that
an introspection-only caller should keep coding against the interface rather
than assume a concrete type. `Open` returns `*magus.Magus`, which also
satisfies `types.WorkspaceRepository` and additionally has `Run`.

`magus.FindRoot(dir)` walks up from `dir` (an empty `dir` is an error; pass
`os.Getwd()` for the current directory) to locate the workspace root, the same walk the CLI performs
before either constructor runs.

## A minimal working example

```go
package main

import (
    "context"
    "fmt"
    "log"

    magus "github.com/egladman/magus"
    _ "github.com/egladman/magus/buzzengine"
)

func main() {
    ctx := context.Background()
    ws, err := magus.Inspect(ctx, "/path/to/workspace", magus.WithSpawnRule(magus.TierCeiling))
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println("workspace root:", ws.Root())
    for _, p := range ws.All() {
        fmt.Printf("project %s (spell=%q, resolved spells=%d)\n", p.Path, p.Spell, len(p.ResolvedSpells))
    }
}
```

The blank import links the engine that evaluates a `magusfile.buzz`. Without
it, `Inspect` on a workspace with a magusfile returns
`*types.EngineNotLinkedError`; see
[linking the Buzz engine](#linking-the-buzz-engine).

## The interface hierarchy: depend on the narrowest role

`types.WorkspaceRepository` (what `Open` and `Inspect` both satisfy) is an
embedding of four smaller interfaces, and `types/repository.go` states the
house rule outright: prefer the narrowest one your code uses.

| Role               | Methods                                                           | Answers                                                   |
| ------------------ | ----------------------------------------------------------------- | --------------------------------------------------------- |
| `WorkspaceReader`  | `Root`, `All`, `Get`, `Graph`, `VCSOptions`, `Where`              | what projects exist, and the project dependency graph     |
| `TargetExpander`   | `ExpandPath`, `ExpandAffected`                                    | which concrete `path:target` pairs a target pattern names |
| `AffectedComputer` | `Affected`, `AffectedFromPaths`                                   | which projects a VCS changeset touches                    |
| `Inspector`        | `List*`, `Evaluate*`, `ClassifyFiles`, `TargetGraph`, `Workspace` | see the axis below                                        |

A function that only reads project facts should take `types.WorkspaceReader`,
not the full repository:

```go
func printProjectCount(r types.WorkspaceReader) {
    fmt.Println("project count:", len(r.All()))
}

// Both a read-only Inspect result and an Open'd *magus.Magus satisfy this.
printProjectCount(ws)
```

Widening a parameter to `types.WorkspaceRepository` because that happens to
be what you have on hand forces every future caller, including a test, to
construct or stub the whole repository just to satisfy a signature that only
calls `Root()` and `All()`.

## The `List` / `Evaluate` / `Classify` axis

This is the organizing idea of the `Inspector` interface. `List*` enumerates
a declaration: cheap, no resolution. `Evaluate*` resolves one (spells
bound, charms patched in) and costs more. `ClassifyFiles`
and `TargetGraph` are their own verbs because neither is a natural fit for
either half.

| Method                      | Reads                                                      | Cost                                                 |
| --------------------------- | ---------------------------------------------------------- | ---------------------------------------------------- |
| `ListProjects`              | declared project facts, project-relative globs             | cheap                                                |
| `ListTargets`               | target name to spell/project vocabulary                    | cheap                                                |
| `ListCharms`                | inverse charm index across every project                   | the most expensive `Inspector` method                |
| `EvaluateProjects`          | resolved spells, workspace-rooted globs                    | resolves every project                               |
| `EvaluateTarget(ctx, t)`    | the full dispatch plan for one `path:target`               | resolves one target                                  |
| `ClassifyFiles(ctx, paths)` | which project owns/declares each path                      | pure glob lookup, cheap even over a whole dirty tree |
| `TargetGraph(ctx)`          | the `ctx.needs` DAG, read statically from magusfile source | never executes a target body                         |

```go
declared, err := ws.ListProjects(ctx)     // fast: what's declared
resolved, err := ws.EvaluateProjects(ctx) // slower: what it resolves to
classified, err := ws.ClassifyFiles(ctx, []string{"magusfile.buzz"})
graph, err := ws.TargetGraph(ctx)
for _, proj := range graph.Projects {
    for _, n := range proj.Nodes {
        fmt.Printf("target %s/%s depends on %v\n", proj.Path, n.Name, n.Dependencies)
    }
}
```

One naming trap on this axis: `ProjectEntry.Sources` (from `ListProjects`)
and `EvaluatedProject.Sources` (from `EvaluateProjects`) are the same field
name carrying different representations: declared, project-relative globs
in the first case, resolved and workspace-rooted (joined against the project
path, magusfile globs folded in) in the second. Reading one where you meant
the other silently feeds every glob-matching call the wrong pattern.

Two graphs are also easy to conflate: `WorkspaceReader.Graph()` returns the
PROJECT dependency graph (project depends on project, from `depends_on`);
`Inspector.TargetGraph()` returns the TARGET graph (target depends on
target, from `ctx.needs`), within and across projects. A caller doing
scheduling or impact analysis typically holds both.

## ctx and cancellation

Every `Inspector` method takes `ctx` first and returns `error` last,
including the read-only `List*` calls. A cancelled walk returns an error
rather than a truncated result: a partial count is indistinguishable from a
workspace that genuinely has that many projects, so magus treats a
cancellation as failure, not as "here is what I got so far":

```go
cctx, cancel := context.WithCancel(ctx)
cancel()
_, err := ws.ListProjects(cctx)
errors.Is(err, context.Canceled) // true
```

The returned error wraps `ctx.Err()` directly, so both
`errors.Is(err, context.Canceled)` and
`errors.Is(err, context.DeadlineExceeded)` hold as expected. Never treat an
empty or short `List*`/`Evaluate*` result as "the workspace has nothing"
without checking the error first.

## What the workspace reads from your process

A library workspace reads no ambient process state. Each thing the CLI takes from its
own process is an option, and leaving one out has a stated result:

| Option                  | What it hands the workspace                                                      | Without it                         |
| ----------------------- | -------------------------------------------------------------------------------- | ---------------------------------- |
| `WithEnv(env)`          | the environment: `MAGUS_*` configuration overrides, the remote cache signing key | no environment; `magus.yaml` alone |
| `WithStderr(w)`         | notices, and a run's text report when no sink is given                           | discarded                          |
| `WithCommandLine(args)` | the command line a project lock's owner record and the broker identity carry     | an empty command line              |
| `WithSpawnRule(rule)`   | the rule every job spawn is held to                                              | `Open` and `Inspect` fail          |

Processes a run starts still inherit your process's environment. `FindRoot("")` is
an error rather than a quiet read of the working directory; pass the directory you
mean.

## Judging a call before it runs

`*magus.Magus` answers what the agent guard would decide about a call, without a host
in the loop:

```go
j, err := m.JudgeCommand(ctx, session, dir, "git push --force")
if err != nil {
    return err // no judgment could be made, such as rules that did not load
}
if j.Decision == "deny" {
    fmt.Println(j.Rule, j.Reason)
}
```

`JudgeWrite(ctx, session, dir, path)` judges a file an edit is about to write. Both
return a `types.GuardJudgment`: one decision, the `Rule` that made it, the `Reason`
written for the agent, and on a deny the `Next` steps that pass. A deny is an answer
with a nil error; the error is for a judgment that could not be made, such as a
`*types.EngineNotLinkedError` when the workspace's guard rules need the engine. A rule
that would ask the person denies, since there is no host to put the question to.

The lease a call acts under rides `ctx` through `types.WithJobLease`, and the session
names what that agent did before, so a fire-once advice fires once and spawn records
count. `Run` judges each target the same way before anything runs, as the
`magus run <target> <project>` line the CLI would be judged by. Pass
`magus.WithSession(id)` to judge the run against a session; without it the run is
judged as a caller no host named. A deny returns a `*types.GuardDeniedError` whose
`Judgment` carries the rule and its next steps, and nothing runs. The SDK never
retries, re-grades or routes around a refusal on your behalf.

## Swapping a part

Three parts take a second implementation through one option each, and only these
three, because each has one:

| Option                | Replaces                                                    | Refuses                                                          |
| --------------------- | ----------------------------------------------------------- | ---------------------------------------------------------------- |
| `WithVCSDriver(d)`    | the built-in VCS the workspace's markers would pick         | a nil driver; one that disagrees with `vcs.name` in `magus.yaml` |
| `WithCacheBackend(b)` | the remote cache tier a magusfile wires                     | a nil backend; a magusfile that also wires one                   |
| `WithObserver(o)`     | nothing: it reports each graph build and query of one `Run` | nothing                                                          |

`WithVCSDriver` and `WithCacheBackend` are `Open` options; `WithObserver` is a
`RunOption`, so two runs on one workspace each report to their own observer. A cache
backend is held to the same trust set as a wired one.

## The Buzz host modules from Go

The `std` package holds the Go side of every Buzz host module, and its exported API is
in the same lock as the root package. The job, graph and trail doors take typed
options: `std.MagusPutJob` a `types.JobPut`, whose nil fields leave the row as it is;
`std.MagusExitJob` a `types.JobResult`; `std.MagusWaitJob` a `*types.JobResult`, nil
for the evidence the exit filed; `std.MagusQuery` a `types.KnowledgeQueryOptions`;
`std.MagusRefs` a `types.KnowledgeRefsOptions`; and `std.MagusTrailRead` a
`types.TrailReadOptions`. Each reads the workspace on `ctx` (`types.WithWorkspace`).
A knowledge answer carries a verdict that keeps unknown apart from absent: zero matches
with an `unknown` verdict means part of the workspace had no symbol index.

## What the SDK leaves out

- No agent loop. The SDK answers what a loop asks (may this call run, who owns this
  path, what is this code, what happened, is it done) and never decides what runs next.
- No model adapter or router; see [scope](../scope.md#where-others-drew-it).
- No `ExpandCwd`: expand a target against the project you name with `ExpandPath`.
- No `Serve` or `Stream`: the daemon and the shell pipeline belong to the CLI.
- No `SetGraphObserver`: pass `WithObserver` to the run you want observed.
- No exported declaration names a type from an `internal/` package; the
  `go-api-generate` target fails on one.

## Testing code that uses the SDK

Test at the narrowest seam your code allows, and move to a real workspace only
for what a fake cannot tell you.

**Unit tests: fake the role.** A function that takes `types.WorkspaceReader`
needs a test double with six methods, not a workspace. `types/gen/mocks`
publishes a mockery mock for each role interface, which suits a test that
asserts on the call itself (which method, with what arguments, how often).
Here `countProjects` is your function, taking a `types.WorkspaceReader`:

```go
func TestCountProjects(t *testing.T) {
    r := mocks.NewMockWorkspaceReader(t)
    r.EXPECT().All().Return([]*types.Project{{Path: "api"}, {Path: "web"}})

    assert.Equal(t, 2, countProjects(r))
}
```

When a test only needs one method to return something, embed the interface and
override that method. Any other method panics on the nil embedded value, which
tells you the code under test reached for more than you thought:

```go
type twoProjects struct{ types.WorkspaceReader }

func (twoProjects) All() []*types.Project {
    return []*types.Project{{Path: "api"}, {Path: "web"}}
}
```

**Integration tests: a real workspace.** `testkit.NewWorkspace(t)` returns a
temporary directory you fill with `Write`, `WriteTree` and `Magusfile`, and
`magus.Inspect` reads it like any other workspace. Inspect opens no cache, so
this is the cheapest way to check that your code reads projects, targets and
the graph correctly:

```go
func TestProjectsOfAMonorepo(t *testing.T) {
    ws := testkit.NewWorkspace(t)
    ws.WriteTree(map[string]string{
        "magusfile.buzz":     "",
        "api/magusfile.buzz": "",
    })

    m, err := magus.Inspect(t.Context(), ws.Root(), magus.WithSpawnRule(magus.TierCeiling))
    require.NoError(t, err)

    assert.Len(t, m.All(), 2)
}
```

**Isolate the test process.** Call `testkit.Main` from `TestMain`, or
`testkit.Isolate(t)` at the top of a test that is not parallel:

```go
func TestMain(m *testing.M) { testkit.Main(m) }
```

Both reduce the environment to an allowlist and move `HOME` and the `XDG_*`
directories under a temporary root. This matters more than it looks, because
the `Output*` lookups (`OutputByRef`, `OutputAttempts`,
`OutputDescriptorByRef`, `OutputKeyInputs`) do not stop at the workspace's own
cache when a ref is missing. They read the session store under
`XDG_STATE_HOME`, then the other checkouts of the same repository, and
`PayloadByRef` reads the primary checkout's store the same way. Without
isolation, the result of a miss depends on what the developer running the tests
did yesterday.
`Isolate` changes process-wide state, so it fails a parallel test; use
`Main` when most of the package's tests run in parallel.

**Running targets: depend on a one-method interface.** `*magus.Magus` has
many methods, and a fake that implements them all is not worth writing.
Declare what you call instead, and `*magus.Magus` satisfies it:

```go
type runner interface {
    Run(ctx context.Context, targets []types.Target, opts ...magus.RunOption) error
}
```

A test then passes a function that records the targets it was handed. Keep a
small number of end-to-end tests for the code that builds those targets.

**A test that opens a magusfile links the engine.** One file of the test
binary imports `_ "github.com/egladman/magus/buzzengine"`, or `Open` fails as
[linking the Buzz engine](#linking-the-buzz-engine) describes. To exercise a
run with no magusfile, register a spell in Go and attach it with `WithSpell`;
see `ExampleWorkspaceRegistry_withSpell`.

**Programmatic spells.** `project.DefaultSpellRegistry()` is process-global, so
two tests that register the same name in parallel race, and a spell left
registered leaks into every later test. `register.Spell(tb, name, opts ...spells.Option)` in
`libs/testkit/register` registers a spell for the duration of `tb` and
returns its name. It takes the options of the `spells` package itself, such as
`spells.WithTargets` and `spells.WithInvoker`. The suffix it adds keeps two
registrations of one name apart, but the registry stays shared: a parallel
test that lists or looks up spells sees another test's spell, so keep those
tests out of `t.Parallel`.

`testkit` and `register` take a `testkit.TB`, the part of `testing.TB` they
call. `*testing.T` and `*testing.B` satisfy it, and so can a small type of your
own where no test is running, such as an `Example`.

## Linking the Buzz engine

> [!IMPORTANT]
> A program that opens a workspace with a magusfile must link the Buzz
> engine. Without it, `Open` and `Inspect` return an error, never a
> workspace missing that magusfile's targets.

Evaluating a `magusfile.buzz` (running `magus\project(...)`, discovering
`export fun` targets, binding spells) needs the Buzz engine. The root package
does not link it, so a program that only builds workspaces in Go never pays
for the VM. A program that loads magusfiles links it with one blank import:

```go
import _ "github.com/egladman/magus/buzzengine"
```

`cmd/magus` makes the same import. Without it, `Open` and `Inspect` on a
workspace where any project has a magusfile return
`*types.EngineNotLinkedError` ([MGS1104](codes/capability/MGS1104.md)), whose
message names the import. A workspace with no magusfile still opens without
the engine.

The other path builds the workspace in Go instead of authoring a magusfile,
through the exported wire API in the root package: `WithSpell`, `WithTarget`,
`WithDependsOn`, and friends, composed via a `WorkspaceRegistry` passed to
`Open`/`Inspect` as an `Option`. A target a program names with `WithTarget`
counts as declared, the way an `export fun` does in a magusfile. Built-in
spells (`go`, `ts`, `rust`, ...) decode from embedded bytecode through
`internal/spell`, which the exported wrapper functions reach on your behalf
even though you cannot import that package directly.
