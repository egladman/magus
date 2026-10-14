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

> [!WARNING]
> `go get github.com/egladman/magus` does not resolve on its own, at any
> tagged version. Read this section before filing that as your own bug.

As of this writing, `go get github.com/egladman/magus` does not resolve on
its own. The module's `go.mod` requires two nested modules:
`github.com/egladman/magus/libs/gopherbuzz` and
`github.com/egladman/magus/libs/diagnostics`, each with its own `go.mod`. Both
carry `v0.1.0` tags (`libs/gopherbuzz/v0.1.0`, `libs/diagnostics/v0.1.0`), but
the root module still requires them at `v0.0.0` and resolves that through local
`replace` directives. Those are not transitive, so a downstream consumer
inherits none of that and hits `unknown revision libs/gopherbuzz/v0.0.0`.

Until the root requires the tagged versions and drops its replaces, clone the
repository (or vendor `libs/gopherbuzz` and `libs/diagnostics` out of it) and
add matching `replace` directives to your own `go.mod`:

```go
require github.com/egladman/magus v0.3.0

replace github.com/egladman/magus/libs/gopherbuzz => /path/to/magus/libs/gopherbuzz
replace github.com/egladman/magus/libs/diagnostics => /path/to/magus/libs/diagnostics
```

Everything below assumes that step is done.

## `Open` vs `Inspect`

Two constructors discover a workspace and return a handle to it. Both walk
up from a root directory, load `magus.yaml`, discover projects, and evaluate
each project's magusfile the same way; the only difference is whether a
content-addressed cache gets built.

```go
ctx := context.Background()

// Inspect: read-only, no cache. Use this for introspection - listing,
// describing, classifying files - and nothing else.
ws, err := magus.Inspect(ctx, "/path/to/workspace")

// Open: builds an on-disk cache and returns the concrete *magus.Magus,
// which adds Run. Use this when you intend to execute targets.
m, err := magus.Open(ctx, "/path/to/workspace")
```

`Inspect` returns `types.WorkspaceRepository`, an interface: a signal that
an introspection-only caller should keep coding against the interface rather
than assume a concrete type. `Open` returns `*magus.Magus`, which also
satisfies `types.WorkspaceRepository` and additionally has `Run`.

`magus.FindRoot(dir)` walks up from `dir` (empty means the current
directory) to locate the workspace root, the same walk the CLI performs
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
    ws, err := magus.Inspect(ctx, "/path/to/workspace")
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
| `TargetExpander`   | `ExpandPath`, `ExpandCwd`, `ExpandAffected`                       | which concrete `path:target` pairs a target pattern names |
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

    m, err := magus.Inspect(t.Context(), ws.Root())
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
