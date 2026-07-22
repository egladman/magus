---
title: "MGS1006: unknown target"
description: Fires when a magusfile references a target that no project declares, whether run from the CLI or wired via a dependency like magus.needs.
tags: [MGS1006, magusfile, targets, needs, depends, authoring]
---

# MGS1006: unknown target

A target was referenced by a name that no project in scope declares. This
happens two ways:

- **From the CLI** - `magus run <name>` (or `magus x <name>`) where `<name>`
  matches no target anywhere in the selected scope.
- **From a magusfile** - a dependency handle such as `ctx.needs(<name>)`
  names a target that does not exist.

```text
[MGS1006] magusfile: unknown target "buld" (registered: build, lint, test)
  see: .../MGS1006.md
```

## Why

A target is an exported function in a magusfile (normalized to a kebab-case
name). Magus resolves a referenced target by that name; when nothing declares
it, there is nothing to run. The message lists the targets that ARE registered
in scope so a typo is obvious at a glance.

Note that during a normal fan-out run (`magus run build`, `magus affected ci`),
a project that simply does not declare the requested target is silently skipped,
not reported as unknown - that is expected in a multi-project workspace. MGS1006
is the terminal case: the name resolves to no target at all in the selected
scope.

## Resolution

- **Typo?** Compare against the `registered:` list in the message, or run
  `magus describe targets` to see every target and the project that declares it.
- **Wrong scope?** The target may live in a project outside the current
  selection. Run from the workspace root, or widen the scope.
- **Not defined yet?** Declare it as an exported function in the project's
  magusfile:

  ```buzz
  export fun build(ctx: magus\Context, _a: [str]) > void { go["go-build"](); }
  ```

- **Dependency handle.** `ctx.needs(x)` takes a target FUNCTION handle in the
  same magusfile; a cross-project dependency uses the imported project's handle
  (`import "project/api" as api; ... ctx.needs(api.build);`). A bare
  unresolved name raises this diagnostic.

## What this is NOT

- **Not a missing spell.** A spell provides ops, not targets. If the name you
  meant is a spell op, invoke it through a target, not directly.
- **Not the empty-affected-set no-op.** `magus affected ci` with no changed
  projects runs nothing and does not raise MGS1006.

## See also

- [targets.md](../../targets.md): the anatomy of a magus target.
- `magus describe targets`: every target and where it is declared.
