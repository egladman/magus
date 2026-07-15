---
title: Dependencies
description: The two dependency mechanisms in magus - magus.needs (target-level, imperative) and depends_on (project-level, declarative) - how they interact, and how a cross-project needs folds into both.
tags: [dependencies, needs, depends_on, cache, affected, cycles, magusfile]
---

# Dependencies

magus has two dependency mechanisms that answer two different questions, and
the story of how they interact is scattered today across getting-started
(`needs`), [workspace.md](workspace.md) (`depends_on`), and
[affected.md](affected.md) (the edges the affected closure walks). This page
owns that story end to end.

## The two mechanisms and the decision rule

- **`magus.needs(...)`** is target-level, imperative, and blocking at its call
  site inside the target body. It says "run X before the rest of my body
  executes" - same-project or cross-project, deduped per invocation, run
  once. See [targets.md](targets.md) for the full grammar.
- **`depends_on`** is project-level, declared in `magus.project`'s options
  map. It says "that project is upstream of me" - an ordering barrier for
  same-target runs, a seed for the affected closure, and an input to the
  cache key. See [workspace.md](workspace.md#depends_on-cross-project-dependencies).

**Rule of thumb:** reach for `needs` inside a magusfile to sequence work
("run `generate` before I `build`"); reach for `depends_on` to declare that
another project's changes affect you, independent of whether any target
calls into it directly. A literal cross-project `needs` gives you both at
once - see the fold, below.

## The fold: a literal cross-project `needs` also declares `depends_on`

A cross-project `magus.needs(alias.target)` (where `alias` is a project
imported at the top of the magusfile) is **statically extracted** and
**unioned into the consuming project's `DependsOn`** at workspace-open time
(`applyCrossProjectDependencies`, called from `Magus.Open`'s `load`). You
declare the dependency once, at the target that actually needs it, and it
counts toward the affected closure and cache-key propagation exactly as if
you had also written a `depends_on` entry - you never write both.

**This fold is literal-only.** The extractor reads the magusfile's AST; it
cannot evaluate a dynamically computed target reference. A `magus.needs`
call built from a variable, a function return, or anything other than a
literal `alias.target` expression is invisible to the static graph, to
`magus describe`, and to the affected set. It still runs correctly at
runtime (`magus.needs` itself has no such restriction), but nothing outside
that one target's execution knows the edge exists. If a dependency needs to
be visible to `affected`/`describe` without being called literally, declare
it via `depends_on` instead.

## What a bare `depends_on` does NOT do

`depends_on` is data, not a call. It never invokes anything by itself:

- It does not run the upstream project's target for you. Something still
  has to call it - either the upstream project's own `ci` composition, or
  a `magus.needs` in the dependent.
- It only orders **same-target** runs within one dispatch (`build` in a
  dependent waits on `build` in its dependency, if both are in the current
  scope) - it does not order arbitrary target pairs.
- It seeds the affected closure and feeds `dep:` lines into the cache key
  (see [cache.md](cache.md#the-cache-key)); that is the entirety of its
  runtime effect.

## Caching interplay

A cache hit on a target means **its body never runs** - so any `magus.needs`
calls inside that body never dispatch either, on a hit. This has two
consequences worth stating plainly:

- **`needs` children are not independently cached.** On a miss, the parent
  target's body runs as an ordinary function call, not through `cache.Run` -
  there is no separate cache entry, hit, or miss for the child dispatch
  itself. The child's own target (if selected directly, elsewhere) has its
  own cache entry; the *call from inside this parent* does not.
- **The key is protected by project-wide source globs, so this is
  safe-but-coarse** (see [cache.md](cache.md#granularity-project-wide-vs-per-target)).
  `baseStep` seeds every target's sources with the union of every bound
  spell's `needs` plus the magusfile, so an under-declared `needs` glob is
  the one way a stale hit can slip through - the coarse baseline is the
  safety margin against exactly that. To attach an input to one target rather
  than widen the whole project, declare it in the body with
  [`magus.inputs`](cache.md#per-target-inputs-and-outputs) - the same literal-first
  static discipline this page's `needs` references follow.

## Both-arms rule: the static graph and a dry run can disagree

The static extractor (`internal/describe/extract.go`) that powers `magus
describe`/`magus graph` sees **both arms** of a charm-conditional `magus.needs`
call (an `if magus.has_charm("cd") { magus.needs(...) } else { magus.needs(...) }`
shows both edges in the graph). A dry run (`magus run --dry-run`) evaluates
the magusfile for real and sees only the **taken** branch, under whichever
charms are active. Both are correct for what they represent: the static graph
is "everything this target could need under some charm," the dry run is
"what this exact invocation needs." They are allowed to disagree, and neither
is a bug when they do.

## Cycle and error behavior

- **Same-project runtime cycle.** A target that (transitively) needs itself
  fails with `buzzpool: dispatch: stack contains "<name>" (cycle detected)` -
  the ancestor stack that catches this also catches a direct self-loop
  (`magus.needs(magus.target.literal("self"))` inside `self`).
- **Cross-project runtime cycle.** Two projects whose `magus.needs` chains
  point back at each other fail with `cross-project cycle: <dir> target
  "<name>"`, detected by the same run's `CrossDispatch` coordinator.
- **Unregistered `depends_on` path.** A `depends_on` entry naming a project
  path that was never discovered/registered fails workspace load with
  `magus: dependency not registered (N unresolved)`, listing each
  `<consumer> -> <dep>` pair with a did-you-mean suggestion when one is close.
- **MGS4004 (undeclared dependency, runtime hint).** Diagnostic, not a load
  error: when `--race` detects a path written by one project and read by
  another that was not in the dispatched scope, it warns "potential
  undeclared dependency" - a signal you may be missing a `depends_on`, not a
  guarantee.

## `needs` targeting: literal, glob, regex - same-project only

`magus.target.literal/glob/regex` build the query `magus.needs` consumes.
Glob and regex queries are **same-project only** (a cross-project edge is
always a literal `alias.target`). A glob with no `*` is **suffix shorthand**,
not a substring or exact match: `magus.target.glob("build")` compiles to
`^.*-build$` and matches `go-build`/`docker-build`, but **not** a target
literally named `build` - write `magus.target.literal("build")` for that.
A glob containing `*` compiles as an ordinary anchored glob instead.

## A service reached via `needs` is supervised, not foregrounded

A [service op](services.md) run **directly** (`magus run dev`) forks in the
foreground and blocks until Ctrl-C. The same service reached as a
`magus.needs` dependency is instead **supervised in the background**:
started, gated on its readiness probe, and shared with any other dependent
that needs the same configuration - the dependent's own body runs without
blocking on the service process itself. See
[Directly run vs. as a dependency](services.md#directly-run-vs-as-a-dependency).

## See also

- [targets.md](targets.md): the `magus.needs`/`magus.target.*` grammar and the
  target-name model these edges resolve against.
- [workspace.md](workspace.md): `depends_on` path resolution and the
  `magus.project` options map it lives in.
- [cache.md](cache.md): the cache key `dep:` lines and the granularity note
  this page's caching section builds on.
- [affected.md](affected.md): the transitive closure these edges feed.
