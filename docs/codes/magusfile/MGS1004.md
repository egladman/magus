---
title: "MGS1004: unreached footprint declaration"
description: Fires when a magus.inputs or magus.outputs call is not statically reachable from a target body, so the declared glob never enters a cache key.
tags: [MGS1004, magusfile, cache, inputs, outputs, doctor]
---

# MGS1004: unreached footprint declaration

`magus doctor` found a `magus.inputs(...)` or `magus.outputs(...)` call that the
static extractor cannot reach from any target body. magus reads these
declarations from the source without running it (a cache hit skips the body
entirely), following a target's body and the helpers it calls by name. A call it
can't reach never enters a cache key.

```text
[MGS1004] 1 magus.inputs/outputs call(s) are not statically reachable from a
target body, so they never enter a cache key; call them directly in the target
body (see .../MGS1004.md)
  magus.inputs in srcGlobs (web/magusfile.buzz:14)
```

## Why

A per-target footprint has to be known *before* the target runs, because on a
cache hit the body never executes. So magus recovers `magus.inputs`/`outputs`
statically: it walks each `export fun` target and the helper functions it calls
by a plain name (`srcGlobs()`), and collects the string-literal globs it finds.

A declaration outside that reach is invisible to the cache:

- a call in a helper that no target (transitively) calls by name - often dead
  code;
- a call reached only through indirection - the identifier used as a value
  (`final f = magus.inputs; f("src/**")`) or dynamic dispatch, which the static
  read cannot follow.

The danger is silent under-declaration: the input you thought you declared is not
in the key, so editing it produces no miss and you replay a stale build. This
check makes that loud. It is the counterpart to the hard load error you get for a
non-literal argument in a *reached* call (`magus.inputs(someVar)`) - that one
magus can see and rejects immediately; an *unreached* call it can only warn about.

This is a **warning**, not a load error: an unreached call may simply be dead
code, which is harmless.

## Resolution

Call `magus.inputs`/`magus.outputs` directly in the target body, or from a helper
the target invokes by name:

```buzz
// Before: the glob lives in a helper nothing calls, so it never keys anything.
fun srcGlobs() > void { magus.inputs("src/**"); }
export fun build(args: [str]) > void { go["go-build"](); }

// After: declared in the body (or a bare-called helper), so it enters the key.
export fun build(args: [str]) > void {
    magus.inputs("src/**");
    go["go-build"]();
}
```

If the flagged call is genuinely dead code, delete it.

## What this is NOT

- **Not a hard error.** Nothing blocks the build; it is a `magus doctor` finding.
- **Not the non-literal-argument error.** A computed argument in a *reachable*
  call (`magus.inputs(x)`) is a magusfile load error, because magus sees the call
  but cannot resolve the glob. MGS1004 is the opposite: magus resolves nothing
  because it never reaches the call.

## See also

- [cache.md](../../cache.md#per-target-inputs-and-outputs): how `magus.inputs` and
  `magus.outputs` declare a target's per-target footprint.
- [dependencies.md](../../dependencies.md): the static-extraction discipline
  magus.inputs shares with `magus.needs`.
