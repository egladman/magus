---
title: Break now, hold still later
order: 19
description: Before 1.0 a wrong name or format is fixed without a migration path; after it, the tool that works the way it worked last year is the product.
tags: [design, principle, compatibility]
---

# Break now, hold still later

Durability is what I want magus to be known for: the tool that works the way it
worked last year, that you put underneath something and stop thinking about.
Go is the model. Code from a decade ago still builds, and that is a maintained
commitment rather than luck.

You earn that by not moving, over years, and holding still is not passive. You
cannot stay put on top of decisions you did not think through. Not moving later
is paid for with forethought now, and with fixing what is wrong while the blast
radius is small. So before 1.0 a name, flag or format that turns out wrong is
renamed, removed or changed outright, with no compatibility shim, no dual
reader and no deprecated alias. Carrying an incoherent name forever costs more
than one breaking release.

The pattern I refuse is the one too many build tools run: ship a half-built
abstraction, push everyone onto it, deprecate it a few releases later, and push
everyone onto the next one. That is what too many hands on a design with too
little confidence in it looks like from the outside. Hammer the abstraction out
first, here, while only this repository pays for the rework, and ask people to
move once, to something you are sure of.

After 1.0 the direction flips, and the
[compatibility contract](../../concepts/compatibility.md) says what is promised:
additive keys, a declared version floor, nothing that vanishes out from under
anyone.

## Enforced by

- `AGENTS.md`: pre-1.0, breaking is fine, and a compat shim, dual reader,
  fallback or `Deprecated:` alias needs Eli's yes first.
- An approved shim carries `compat(until: <condition>):`.
- [Compatibility](../../concepts/compatibility.md) states what holds after 1.0.

## Good

The rename lands whole, in one change, with the old name gone:

```text
magus session hook is now `magus shell`, which takes the same flags and stdin.
Re-run `magus agent harness install` to rewrite host wiring that still calls the old path
```

## Bad

```go
// Deprecated: use Shell. Kept so existing hooks keep working.
func SessionHook(args []string) error { return Shell(args) }
```

At 0.x nobody depends on the old name yet, and the alias is how somebody comes
to. It doubles what every reader and every agent has to learn.

## See also

- [Delete before you add](delete-before-you-add.md)
- [Compatibility](../../concepts/compatibility.md)
