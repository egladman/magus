---
title: "MGS6001: charm patch does not apply to the command"
description: Fires when magus describe target previews a charm whose JSON Patch is valid in shape but cannot apply to the target's argv, so the charm is dead on that target.
tags: [MGS6001, charms, json-patch, rfc-6902, describe, argv, preview]
---

# MGS6001: charm patch does not apply to the command

`magus describe target <name>:<charm>` was asked to preview the charm-applied
command, but applying the charm's [JSON Patch](../../../concepts/charms.md#the-mechanism-a-json-patch-over-the-argv)
to the target's argv failed. The patch is well-formed (it passed the
shape check every charm declaration goes through) yet does not fit this
target's actual arguments, so the charm would change nothing here.

```text
[MGS6001] target "lint" in project ".": charm(s) [rw] do not apply to spell "go"'s
command (json-patch: index 7 out of range for argv of length 4)
  see: .../MGS6001.md
```

## Why

A charm is an RFC 6902 patch over a target's argument vector (see
[charms.md](../../../concepts/charms.md)). Magus validates a charm's _shape_ when the spell
loads: the op is one of the six, the path is a `/`-rooted JSON Pointer, a
`move`/`copy` carries a `from`. That check cannot know whether the pointer
resolves, because it does not have the target's argv in hand.

The pointer is only resolved when the charm is _applied_ to a concrete command.
Two well-formed patches fail at that point:

- **An out-of-range index.** `{"op": "add", "path": "/7", ...}` against a
  four-element argv has nowhere to land. This is what a hand-written positional
  patch drifts into when the base command changes and the counted index is not
  updated (the reason the [`charm` constructors](../../../concepts/charms.md#the-charm-constructor-reference)
  anchor by value instead).
- **A failing `test` op.** `{"op": "test", "path": "/1", "value": "run"}` asserts
  the element is still where the author expected; when it is not, the patch is
  rejected on purpose.

Before this diagnostic, `magus describe` silently dropped the `command:` line for
such a target: the preview rendered without the charm and without a word, so you
could not tell a charm had failed to apply until a real run. Magus now surfaces
it, because a charm that is dead on a target is exactly the kind of
deterministic-but-invisible gap `describe` exists to close.

## Resolution

Fix the charm declaration so its patch fits the target's argv:

- **Prefer a value anchor over a counted index.** Replace a literal `"/7"` with
  a constructor that resolves the position at author time
  (`charm.after(args, "run", [...])`), so the pointer tracks the argv instead of
  a stale count. See [the constructor reference](../../../concepts/charms.md#the-charm-constructor-reference).
- **Check the base command.** Run `magus describe target <name>` with no charm
  to see the argv the charm must patch, then confirm the index or anchor the
  charm targets actually exists in it.
- **Relax or remove a stale `test` op.** A `test` op guards a position; if the
  base argv legitimately changed, update the asserted value or drop the guard.

## What this is NOT

- **Not a malformed patch.** A patch with a bad op name or a non-rooted path is
  rejected earlier, at spell load, not here. MGS6001 is specifically a
  _well-formed patch that does not apply_ to this target.
- **Not a runtime failure.** `describe` executes nothing. This is a static
  preview diagnostic; the same mismatch would also fail a real run, which is why
  surfacing it early is worthwhile.

## See also

- [charms.md](../../../concepts/charms.md): the anatomy of a charm and its JSON-Patch model.
- [charms.md § Previewing the rendered command](../../../concepts/charms.md#previewing-the-rendered-command):
  what `magus describe target` renders.
- `magus describe target <name>`: the base command a charm patches, with no charm applied.
