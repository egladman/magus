---
title: Dependencies
order: 3
description: The two dependency mechanisms in magus - magus\needs (target-level, imperative) and depends_on (project-level, declarative) - how they interact, and how a cross-project needs folds into both.
tags: [dependencies, needs, depends_on, cache, affected, cycles, magusfile]
---

# Dependencies

magus has two dependency mechanisms that answer two different questions, and
the story of how they interact is scattered today across getting-started
(`needs`), [workspace.md](workspace.md) (`depends_on`), and
[affected.md](workspace/affected.md) (the edges the affected closure walks). This page
owns that story end to end.

## The two mechanisms and the decision rule

- **`ctx.needs(...)`** is target-level, imperative, and blocking at its call
  site inside the target body. It says "run X before the rest of my body
  executes" - same-project or cross-project, deduped per invocation, run
  once. See [targets.md](targets.md) for the full grammar.
- **`depends_on`** is project-level, declared in `magus\project`'s options
  map. It says "that project is upstream of me" - an ordering barrier for
  same-target runs, a seed for the affected closure, and an input to the
  cache key. See [workspace.md](workspace.md#depends-on-cross-project-dependencies).

**Rule of thumb:** reach for `needs` inside a magusfile to sequence work
("run `generate` before I `build`"); reach for `depends_on` to declare that
another project's changes affect you, independent of whether any target
calls into it directly. A cross-project `needs` gives you both at
once - see the fold, below.

## The fold: a cross-project `needs` also declares `depends_on`

A cross-project `ctx.needs(alias.target)` (where `alias` is a project
imported at the top of the magusfile, whose exported targets it binds as
callable handles) is **statically extracted** and **unioned into the
consuming project's `DependsOn`** at workspace-open time
(`applyCrossProjectDependencies`, called from `Magus.Open`'s `load`). You
declare the dependency once, at the target that actually needs it, and it
counts toward the affected closure and cache-key propagation exactly as if
you had also written a `depends_on` entry - you never write both.

**The fold is a static read, so a computed edge is invisible.** The extractor
reads the magusfile's AST; it resolves a same-project target passed by
reference (`ctx.needs(build)`), a cross-project handle passed as a member
access (`ctx.needs(alias.target)`), and each literal pattern given to
`magus\glob` inside a `magus\needs`. What it cannot evaluate is a _computed_ dependency - a
handle stored in a variable, returned from a function, or otherwise built at
runtime. Such a `magus\needs` call is invisible to the static graph, to
`magus describe`, and to the affected set. It still runs correctly at runtime
(`magus\needs` itself has no such restriction), but nothing outside that one
target's execution knows the edge exists. If a dependency needs to be visible
to `magus affected`/`magus describe` without being passed as a plain handle, declare it
via `depends_on` instead.

## What a bare `depends_on` does NOT do

`depends_on` is data, not a call. It never invokes anything by itself:

- It does not run the upstream project's target for you. Something still
  has to call it - either the upstream project's own `ci` composition, or
  a `magus\needs` in the dependent.
- It only orders **same-target** runs within one dispatch (`build` in a
  dependent waits on `build` in its dependency, if both are in the current
  scope) - it does not order arbitrary target pairs.
- It seeds the affected closure and feeds `dep:` lines into the cache key
  (see [cache.md](cache.md#the-cache-key)); that is the entirety of its
  runtime effect.

## Caching interplay

A cache hit on a target means **its body never runs** - so any `magus\needs`
calls inside that body never dispatch either, on a hit. This has two
consequences worth stating plainly:

- **`needs` children are not independently cached.** On a miss, the parent
  target's body runs as an ordinary function call, not through `cache.Run` -
  there is no separate cache entry, hit, or miss for the child dispatch
  itself. The child's own target (if selected directly, elsewhere) has its
  own cache entry; the _call from inside this parent_ does not.
- **The key is protected by project-wide source globs, so this is
  safe-but-coarse** (see [cache.md](cache.md#granularity-project-wide-vs-per-target)).
  `baseStep` seeds every target's sources with the union of every bound
  spell's `needs` plus the magusfile, so an under-declared `needs` glob is
  the one way a stale hit can slip through - the coarse baseline is the
  safety margin against exactly that. To attach an input to one target rather
  than widen the whole project, declare it in the body with
  [`magus\inputs`](cache.md#per-target-inputs-and-outputs), whose literal globs
  are read from the AST the same way these `needs` edges are.

## Both-arms rule: the static graph and a dry run can disagree

The static extractor (`internal/describe/extract.go`) that powers `magus
describe`/`magus graph` sees **both arms** of a charm-conditional `magus\needs`
call (an `if ctx.has_charm("cd") { ctx.needs(...) } else { ctx.needs(...) }`
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
  (`ctx.needs(self)` inside `self`).
- **Cross-project runtime cycle.** Two projects whose `magus\needs` chains
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

## `needs` and `glob`: functions and patterns, same-project globs

`magus\needs` takes target **functions**: a same-project target passed by
reference (`ctx.needs(build, test)`) or a cross-project handle a project
import binds (`ctx.needs(alias.target)`), or the list of handles `magus\glob`
resolves a pattern to. It never takes a string or a query object; a mistyped
identifier is an undefined variable and fails at **load**, not at run time. For
patterns, resolve them to handles with `magus\glob` and pass the result to
`magus\needs`.

`ctx.glob(pattern...)` is **same-project only** (a cross-project edge is always a
handle `alias.target`) and resolves to the **handles** of the matching registered
targets, which you feed to `magus\needs` (`ctx.needs(ctx.glob("*-generate"))`).
A pattern that matches nothing yields no handles, so needs of it is a no-op. Only
exported-function targets carry a handle; depend on a spell-provided op directly.

### Pattern forms

Every form below is runnable: step 7 of the guided tour,
[Globs: gather a target family, minus one](../tour/index.html#step-7), builds this exact family in
the browser and lets you edit the patterns and re-run them.

Three forms, and they compose in one call:

| Form | Example | Compiles to | Matches |
| --- | --- | --- | --- |
| Suffix shorthand | `"build"` | `^.*-build$` | `go-build`, `docker-build` |
| Glob | `"*-generate"` | `^.*-generate$` | `md-generate`, `site-generate` |
| Negation | `"!site-generate"` | `^site-generate$`, subtracted | everything else the includes matched |

Negation subtracts from the union of the includes, so order never matters:
`("*-generate", "!site-generate")` and `("!site-generate", "*-generate")` select
the same set. Results are deduplicated and sorted, so a name matched by two
patterns runs once and the dispatch order does not vary run to run.

### Every form, against one target set

Paste this into a magusfile and run `magus ls` to see the family, then
`magus run <umbrella> --dry-run` to watch each pattern resolve. Every example
below assumes exactly these five targets:

```buzz
import "magus";
magus\project({});

export fun md_generate(ctx: magus\Context, args: [str]) > void {}
export fun site_generate(ctx: magus\Context, args: [str]) > void {}
export fun vendor_generate(ctx: magus\Context, args: [str]) > void {}
export fun go_build(ctx: magus\Context, args: [str]) > void {}
export fun generate(ctx: magus\Context, args: [str]) > void {}
```

Each umbrella below is a real target you can export alongside them. The comment
on each is exactly what `ctx.glob` resolves to.

```buzz
// GLOB: the whole -generate family.
//   -> md-generate, site-generate, vendor-generate
export fun all_generate(ctx: magus\Context, args: [str]) > void {
    ctx.needs(ctx.glob("*-generate"));
}

// SUFFIX SHORTHAND: identical to the glob above. A bare word means "-word" at
// the end of a name. Note what is NOT in the result: the target named
// `generate`. That is what makes this safe to write inside `generate` itself.
//   -> md-generate, site-generate, vendor-generate
export fun all_generate_shorthand(ctx: magus\Context, args: [str]) > void {
    ctx.needs(ctx.glob("generate"));
}

// NEGATION, one name: the family minus a single member.
//   -> md-generate, vendor-generate
export fun generate_fast(ctx: magus\Context, args: [str]) > void {
    ctx.needs(ctx.glob("*-generate", "!site-generate"));
}

// NEGATION, a glob: the family minus a sub-family.
//   -> md-generate, site-generate
export fun generate_first_party(ctx: magus\Context, args: [str]) > void {
    ctx.needs(ctx.glob("*-generate", "!vendor-*"));
}

// ORDER DOES NOT MATTER: the exclusion applies to the union of the includes,
// so this is the same set as generate_fast above.
//   -> md-generate, vendor-generate
export fun generate_fast_reordered(ctx: magus\Context, args: [str]) > void {
    ctx.needs(ctx.glob("!site-generate", "*-generate"));
}

// SEVERAL INCLUDES: unioned, then deduplicated and sorted.
//   -> go-build, md-generate, site-generate, vendor-generate
export fun everything(ctx: magus\Context, args: [str]) > void {
    ctx.needs(ctx.glob("*-generate", "*-build"));
}
```

Run these against a live workspace in the tour's
[glob step](../tour/index.html#step-7); `magus run <umbrella> --dry-run` prints the resolved set
without executing anything.

And the four that surprise people, each one a no-op rather than an error:

```buzz
// ONLY A NEGATION: nothing. Subtracting from an empty set is empty - it does
// NOT mean "everything else".
//   -> (no handles)
export fun nothing_at_all(ctx: magus\Context, args: [str]) > void {
    ctx.needs(ctx.glob("!site-generate"));
}

// NEGATION IS EXACT, NOT SHORTHAND: "!generate" removes the target literally
// named `generate`, which the include never selected anyway. Nothing is
// subtracted. To drop the family, write "!*-generate".
//   -> md-generate, site-generate, vendor-generate
export fun negation_is_exact(ctx: magus\Context, args: [str]) > void {
    ctx.needs(ctx.glob("*-generate", "!generate"));
}

// A REGEX IS LITERAL TEXT: patterns are escaped before "*" is translated, so
// this matches a target whose name is that whole string. There is none.
//   -> (no handles)
export fun regex_does_nothing(ctx: magus\Context, args: [str]) > void {
    ctx.needs(ctx.glob("^(?!site-).*-generate$"));
}

// A PATTERN THAT MATCHES NOTHING is not an error; needs of no handles is a
// no-op, which is what lets an umbrella survive a family being renamed.
//   -> (no handles)
export fun future_family(ctx: magus\Context, args: [str]) > void {
    ctx.needs(ctx.glob("*-lint"));
}
```

### Three rules worth knowing before you need them

**Suffix shorthand never matches the bare name.** `ctx.glob("build")` resolves
`go-build` and `docker-build`, never a target literally named `build`. That is
deliberate rather than incidental: it is what makes `ctx.needs(ctx.glob("generate"))`
safe to write _inside_ the `generate` target. Widening the shorthand to also match
bare names would turn every umbrella target into a self-dependency. To depend on
`build` itself, pass the function: `ctx.needs(build)`.

**A negation is a name or a glob, never suffix shorthand.** `"!md-generate"`
excludes the target actually called `md-generate`. If negation used the include
rule it would compile to `^.*-md-generate$` and quietly subtract nothing, which is
the one outcome a subtraction must never produce. To exclude a family, spell it
the way you would include one: `"!*-generate"`.

**Patterns that are only negations select nothing.** `ctx.glob("!site-generate")`
resolves to no handles. Subtracting from an empty set is empty, not "everything
else" - a glob that silently grew to the whole workspace because someone deleted
its one positive pattern is a worse failure than one that matches nothing.

### Globs, not regexes

The pattern surface is glob. Every pattern is `QuoteMeta`'d before `*` is
translated, so an authored regex is matched as literal text: `"^(?!site-).*-generate$"`
matches a target with that exact name, which is to say nothing. Two reasons it
stays that way. A pattern that is sometimes glob and sometimes regex has no safe
reading for `*`. And the engine underneath is Go's RE2, which has no lookaround at
all, so the expression people reach for first - "everything ending in `-generate`
except this one" - is not expressible as a single regex regardless. Negation exists
because that is the actual use case, and it is expressible directly.

One matcher serves all three readers of a pattern - the runtime dispatch, `magus
run --dry-run`, and the static extractor behind `magus describe`/`magus graph`
(`types.MatchTargetPatterns`). They agree by construction rather than by three
implementations being kept in step.

## A service reached via `needs` is supervised, not foregrounded

A [service op](services.md) run **directly** (`magus run dev`) forks in the
foreground and blocks until Ctrl-C. The same service reached as a
`magus\needs` dependency is instead **supervised in the background**:
started, gated on its readiness probe, and shared with any other dependent
that needs the same configuration - the dependent's own body runs without
blocking on the service process itself. See
[Directly run vs. as a dependency](services.md#directly-run-vs-as-a-dependency).

## See also

- [targets.md](targets.md): the `magus\needs`/`magus\glob` grammar and the
  target-name model these edges resolve against.
- [workspace.md](workspace.md): `depends_on` path resolution and the
  `magus\project` options map it lives in.
- [cache.md](cache.md): the cache key `dep:` lines and the granularity note
  this page's caching section builds on.
- [affected.md](workspace/affected.md): the transitive closure these edges feed.
- [The guided tour, step 7](../tour/index.html#step-7): the pattern forms above, runnable and
  editable in the browser - including the negation that keeps one member of a family
  out of its umbrella.
