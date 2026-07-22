---
title: "MGS1005: redundant footprint glob"
description: Fires when a per-target magus.inputs or magus.outputs glob is already declared project-wide, making it a no-op under the additive footprint model.
tags: [MGS1005, magusfile, cache, inputs, outputs, doctor]
---

# MGS1005: redundant footprint glob

`magus doctor` found a per-target `ctx.inputs(...)` or `ctx.outputs(...)`
glob that is already declared project-wide - either in the project's `sources`
/`outputs` options or contributed by a bound spell. Under the additive footprint
model, the per-target copy adds nothing.

```text
[MGS1005] 1 per-target magus.inputs/outputs glob(s) duplicate a project-wide
declaration (a no-op under the additive model); drop the duplicate (see
.../MGS1005.md)
  build: ctx.inputs("src/**") already in project sources
```

## Why

A target's cache footprint is the **union** of three layers: the globs a bound
spell contributes, the project-wide `sources`/`outputs`, and the per-target
`magus.inputs`/`magus.outputs`. Per-target declarations only ever _add_ to the
footprint - they never shrink the project-wide baseline.

So a per-target glob that repeats one already present project-wide changes
nothing about the cache key or the snapshot set. It is harmless, but it reads as
if it narrowed or specialized the target's footprint when it did not - a
misleading no-op worth removing.

This most often happens by copy-paste: the same glob declared both in
`magus.project({sources = [...]})` and in a target body, or a `magus.inputs`
glob that a bound spell's `needs` already covers.

This is a **warning**, not a load error: a duplicate is a no-op, not a fault.

## Resolution

Keep the declaration in exactly one place, chosen by scope:

- if the glob is relevant to **every** target, keep the project-wide
  `sources`/`outputs` and drop the per-target copy;
- if it is relevant to **one** target, drop the project-wide declaration and keep
  the `magus.inputs`/`magus.outputs` in that target's body.

```buzz
// Before: "src/**" declared twice - the per-target copy is a no-op.
magus.project({sources = ["src/**"]});
export fun build(ctx: magus\Context, args: [str]) > void {
    ctx.inputs("src/**");
    go["go-build"]();
}

// After: one home. Here it affects every target, so keep it project-wide.
magus.project({sources = ["src/**"]});
export fun build(ctx: magus\Context, args: [str]) > void { go["go-build"](); }
```

## What this is NOT

- **Not a hard error.** The duplicate is a no-op; nothing blocks the build.
- **Not subsumption-aware.** This check matches globs exactly. A per-target glob
  that is _subsumed_ by a broader project-wide pattern (`src/config.go` under
  `**/*.go`) is also redundant but is not flagged here.

## See also

- [cache.md](../../cache.md#per-target-inputs-and-outputs): the three footprint
  layers and the union model.
- [cache.md](../../cache.md#granularity-project-wide-vs-per-target): choosing
  project-wide vs per-target declarations.
