---
title: "MGS1003: bespoke phase-fragment target name"
description: Fires when a magusfile declares a target named after a static-analysis or formatting subset (typecheck, vet, audit, security, style, prettify) instead of composing it into lint or format.
tags: [MGS1003, magusfile, targets, lint, format, doctor, naming]
---

# MGS1003: bespoke phase-fragment target name

`magus doctor` found a target whose normalized name is one of `typecheck`,
`type-check`, `vet`, `audit`, `security`, `style`, or `prettify`. Each of these
names a subset of an existing canonical phase - static analysis or formatting -
rather than a phase of its own.

```text
[MGS1003] 1 target name(s) name static analysis or formatting rather than a
phase of their own; compose the op into lint (or format) so `magus affected ci`
covers it (docs/targets.md#the-target-name, see .../MGS1003.md)
  typecheck: "typecheck" in web/magusfile.buzz
```

## Why

`lint` is defined as "static analysis, type-check" (see
[targets.md](../../targets.md#the-target-name)): a Go project's `go vet` and a
TypeScript project's `tsc --noEmit` both compose into `lint`, the same way
eslint and golangci-lint do. `format` plays the analogous role for style/prettify
tools. A standalone `typecheck` (or `vet`, `audit`, `security`, `style`,
`prettify`) target carves one tool's check out of that composition into its own
name.

The practical cost is that `ci` is composed from the canonical phases via
`magus.needs`. A bespoke `typecheck` target sitting beside `lint` is invisible to
any `ci` that only needs `lint` - the type-check silently never runs in CI unless
someone remembers to add it separately. Folding the op into `lint` means it rides
along automatically wherever `lint` already does.

This is a **warning**, not a magusfile load error: unlike [MGS1002](MGS1002.md)
(a spell shadow, unresolvable without a rename or an acknowledgment), a bespoke
name is a valid, working target - just one that's easy to forget in a pipeline.
The escape hatch is simply keeping the name if that's a deliberate choice for
your workspace.

## Resolution

Move the tool invocation into `lint` (or `format` for prettify/style) instead of
a standalone target:

```buzz
// Before: a standalone typecheck target ci must remember to add separately.
export fun typecheck(ctx: magus\Context, args: [str]) > void {
    ts["tsc"]();
}

// After: tsc composes into lint, alongside eslint - one target ci already needs.
export fun lint(ctx: magus\Context, args: [str]) > void {
    ts["tsc"]();
    ts["eslint"]();
}
```

If your workspace genuinely wants the check split out for a reason doctor
can't see (a slow type-check run separately from fast lint, say), keep the
name - this check only flags it as a naming choice worth a second look.

## What this is NOT

- **Not a hard error.** The target still runs; nothing blocks the build. It is
  a `magus doctor` finding, not a magusfile load failure.
- **Not the canonical-name litmus test itself.** See
  [targets.md](../../targets.md#the-target-name) for the full "when does a name
  earn canonical status" reasoning; this check only flags the specific set of
  known static-analysis/formatting fragments, not any custom target name.

## See also

- [targets.md](../../targets.md#the-target-name): the seven canonical target
  names and what composes into `lint`/`format`.
- `magus describe targets`: lists every target and which spell ops compose into it.
