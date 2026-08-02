---
title: "MGS1005: redundant footprint glob"
description: Fires when a per-target magus\outputs glob is already declared project-wide, making it redundant.
tags: [MGS1005, magusfile, cache, outputs, doctor]
---

# MGS1005: redundant footprint glob

`magus doctor` found a per-target `ctx.writesFiles(...)` glob already declared in
the project's `outputs` option. The per-target copy adds nothing.

```text
[MGS1005] 1 per-target magus.outputs glob(s) duplicate a project-wide
declaration; drop the duplicate (see
.../MGS1005.md)
  build: ctx.writesFiles("dist/**") already in project outputs
```

## Why

Outputs are always additive: the project owns its declared outputs, and a target
declaring the same glob does not make that output more precise. This is usually
copy-paste from a project-wide declaration.

This is a **warning**, not a load error: a duplicate is a no-op, not a fault.

## Resolution

Keep the output declaration in exactly one place, chosen by scope:

- if the glob is relevant to **every** target, keep the project-wide
  `sources`/`outputs` and drop the per-target copy;
- if it is relevant to **one** target, drop the project-wide declaration and keep
  `ctx.writesFiles(...)` in that target's body.

```buzz
// Before: "dist/**" declared twice - the per-target copy is a no-op.
magus\project({outputs = ["dist/**"]});
export fun build(ctx: magus\Context, args: [str]) > void {
    ctx.writesFiles("dist/**");
    go["go-build"]();
}

// After: one home. Here it affects every target, so keep it project-wide.
magus\project({outputs = ["dist/**"]});
export fun build(ctx: magus\Context, args: [str]) > void { go["go-build"](); }
```

## What this is NOT

- **Not a hard error.** The duplicate is a no-op; nothing blocks the build.
- **Not about inputs.** An explicit `ctx.readsFiles(...)` list narrows the target's
  cache footprint, so repeating a project source there is valid.
- **Not subsumption-aware.** This check matches globs exactly. A per-target glob
  that is _subsumed_ by a broader project-wide pattern (`src/config.go` under
  `**/*.go`) is also redundant but is not flagged here.

## See also

- [cache.md](../../../concepts/cache.md#per-target-inputs-and-outputs): the three footprint
  layers and the union model.
- [cache.md](../../../concepts/cache.md#granularity-project-wide-vs-per-target): choosing
  project-wide vs per-target declarations.
