---
title: "MGS1001: no ci target defined"
description: Fires when magus ci runs but no project in the selected scope declares a ci target, meaning the CI anchor would silently do nothing.
tags: [MGS1001, magusfile, ci, targets, doctor, anchor, affected]
---

# MGS1001: no ci target defined

`magus ci` (or `magus affected ci`, `magus affected --plan`, `magus x` ci) was
asked to run the `ci` target, but no project in the selected scope declares one.

```text
[MGS1001] no "ci" target defined in the selected project(s); it is the anchor
"magus affected ci" and "magus affected --plan" key off, so this run would do nothing
  see: .../MGS1001.md
```

## Why

`ci` is an ordinary magusfile target. Magus does not hardcode a CI chain.
You compose the gate yourself with `magus.needs`, ordering the stages your
workspace needs (build, test, lint, ...).

It is the one target magus treats as the CI anchor: `magus
affected ci` and `magus affected --plan` key off it to decide what the affected
set should build and gate. Every other target fans out and silently skips
projects that don't declare it. That default suits `build`/`test`/`lint`, but
for `ci` it means a workspace with no `ci` target anywhere would let `magus ci`
exit `0` having run nothing, reporting a green check that checked nothing.

Magus fails fast instead. Both paths enforce the same rule:

- **Run time.** `Magus.RunCI` returns this diagnostic before executing anything
  when the scope has projects but none declare `ci`. (An empty scope, such as
  `magus affected ci` with no changes, stays a legitimate no-op and is left
  alone.)
- **`magus doctor`.** The `ci target` check fails when no project in the
  workspace declares `ci`, surfacing the gap before CI ever runs it.

## Resolution

Define a `ci` target in your magusfile and compose the stages with
`magus.needs`:

```buzz
export fun ci(ctx: magus\Context, _a: [str]) > void {
    ctx.needs(build, test, lint);
}
```

Run `magus describe targets` to see the stages available to compose. The name
matches case- and delimiter-insensitively (magus normalizes `CI`/`Ci` to `ci`),
so any casing of the declaration is detected.

In a multi-project workspace the target only needs to exist in one project
for the anchor to resolve. Declare it wherever you compose the workspace gate.

## What this is NOT

- **Not a missing-spell error.** `ci` is never a spell op; it lives in the
  magusfile and composes spell-backed targets via `magus.needs`. Adding a
  spell will not satisfy this check.
- **Not triggered by an empty affected set.** `magus affected ci` with no
  changed projects is a real no-op and does not raise MGS1001. The diagnostic
  fires only when the scope has projects yet none declare `ci`.

## See also

- [targets.md](../../../concepts/targets.md): the anatomy of a magus target.
- [spells.md](../../../concepts/spells.md) § Spells vs Targets: why `ci` is a target, not a spell.
- `magus describe targets`: lists the stages you can compose into `ci`.
