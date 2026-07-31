---
title: The Go SDK
description: Use magus as a Go library instead of the CLI - Open vs Inspect, the interface hierarchy, the List/Evaluate/Classify axis, ctx and cancellation, and what the SDK does not give you yet.
tags: [go, sdk, library, api, embedding, import, package, reference]
---

# The Go SDK

This page is for a Go developer who wants to call magus from their own
program - `import "github.com/egladman/magus"` - rather than shelling out to
the `magus` binary. If you have never run `magus` and do not care to, this is
the page for you; the [CLI reference](cli.md) describes the same domain from
the other side.

## Before you `go get` it

> [!WARNING]
> `go get github.com/egladman/magus` does not resolve on its own, at any
> tagged version. Read this section before filing that as your own bug.

As of this writing, `go get github.com/egladman/magus` does not resolve on
its own. The module's `go.mod` requires two nested modules -
`github.com/egladman/magus/libs/gopherbuzz` and
`github.com/egladman/magus/libs/diag`, each with its own `go.mod` - and
neither has a tagged release (`libs/gopherbuzz/vX.Y.Z` /
`libs/diag/vX.Y.Z`) yet. The root repository resolves them with local
`replace` directives, which are not transitive, so a downstream consumer
inherits none of that and hits `unknown revision libs/gopherbuzz/v0.0.0`.

Until those get their own tags, clone the repository (or vendor
`libs/gopherbuzz` and `libs/diag` out of it) and add matching `replace`
directives to your own `go.mod`:

```
require github.com/egladman/magus v0.3.0

replace github.com/egladman/magus/libs/gopherbuzz => /path/to/magus/libs/gopherbuzz
replace github.com/egladman/magus/libs/diag => /path/to/magus/libs/diag
```

Everything below assumes that step is done.

## Open vs Inspect

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

`Inspect` returns `types.WorkspaceRepository`, an interface - a signal that
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

Against a workspace whose only project is a bare `magusfile.buzz` declaring
one `export fun build`, this prints:

```
workspace root: /path/to/workspace
project . (spell="", resolved spells=0)
```

`resolved spells=0` is not a bug in the example - see
[what this SDK does not give you](#what-this-sdk-does-not-give-you) before
you go looking for why `build` never runs.

## The interface hierarchy: depend on the narrowest role

`types.WorkspaceRepository` - what `Open` and `Inspect` both satisfy - is an
embedding of four smaller interfaces, and `types/repository.go` states the
house rule outright: prefer the narrowest one your code actually uses.

| Role | Methods | Answers |
|---|---|---|
| `WorkspaceReader` | `Root`, `All`, `Get`, `Graph`, `VCSOptions`, `Where` | what projects exist, and the project dependency graph |
| `TargetExpander` | `ExpandPath`, `ExpandCwd`, `ExpandAffected` | which concrete `path:target` pairs a target pattern names |
| `AffectedComputer` | `Affected`, `AffectedFromPaths` | which projects a VCS changeset touches |
| `Inspector` | `List*`, `Evaluate*`, `ClassifyFiles`, `TargetGraph`, `Workspace` | see the axis below |

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
be what you have on hand forces every future caller - including a test - to
construct or stub the whole repository just to satisfy a signature that only
calls `Root()` and `All()`.

## The List / Evaluate / Classify axis

This is the organizing idea of the `Inspector` interface. `List*` enumerates
a declaration - cheap, no resolution. `Evaluate*` resolves one - spells
bound, claims applied, charms patched in - and costs more. `ClassifyFiles`
and `TargetGraph` are their own verbs because neither is a natural fit for
either half.

| Method | Reads | Cost |
|---|---|---|
| `ListProjects` | declared project facts, project-relative globs | cheap |
| `ListTargets` | target name to spell/project vocabulary | cheap |
| `ListCharms` | inverse charm index across every project | the most expensive `Inspector` method |
| `EvaluateProjects` | resolved spells, workspace-rooted globs | resolves every project |
| `EvaluateTarget(ctx, t)` | the full dispatch plan for one `path:target` | resolves one target |
| `ClassifyFiles(ctx, paths)` | which project owns/declares each path | pure glob lookup, cheap even over a whole dirty tree |
| `TargetGraph(ctx)` | the `ctx.needs` DAG, read statically from magusfile source | never executes a target body |

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
name carrying different representations - declared, project-relative globs
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
rather than a truncated result - a partial count is indistinguishable from a
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

## What this SDK does not give you

> [!IMPORTANT]
> A magusfile's custom targets are visible but not runnable through this
> SDK. Read this before you build anything that depends on dispatching one.

**Buzz magusfile evaluation is not reachable from outside this module.**
Evaluating a `magusfile.buzz` - running `magus.project(...)`, discovering
`export fun` targets, binding spells - requires the Buzz interpreter engine
to be linked in, and that link only happens through two `internal/` packages
(`internal/interp/engine/buzz`, `internal/interp/bindings`) that only
`cmd/magus`, built inside this repository, blank-imports. `internal/`
packages are unreachable from any other module's import graph, so a program
that imports `github.com/egladman/magus` as a dependency can discover
projects and read a magusfile's static target graph
(`Inspector.TargetGraph`, which parses source without executing it), but it
cannot make magus dispatch a target that magusfile declares - `ListTargets`
will not list it, no spell gets attached, and attempting to run it is a
silent no-op rather than an error. There is currently no exported way to
even ask "is magusfile evaluation available in this process" - you have to
know this limitation going in.

The escape hatch: build the workspace programmatically instead of authoring
a magusfile, using the exported wire API in the root package -
`WithRegisteredSpell`, `WithTarget`, `WithClaim`, and friends, composed via
a `WorkspaceRegistry` passed to `Open`/`Inspect` as an `Option`. Built-in
spells (`go`, `ts`, `rust`, ...) decode from embedded bytecode through
`internal/spell`, which the exported wrapper functions can reach on your
behalf even though you cannot import that package directly. This path gets
you real spell execution without a magusfile; it does not get you
arbitrary Buzz-authored targets.

**Nothing else in this doc is a workaround for that gap.** If your workspace
model is "a set of projects bound to built-in spells, described in Go," the
SDK covers you end to end. If it depends on a hand-authored magusfile's
custom targets, only the CLI (or a program that vendors and links the
interpreter packages the way `cmd/magus` does, which requires being inside
this module) can run it today.
