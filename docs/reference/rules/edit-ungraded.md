---
title: "edit-ungraded: a rewrite a magus process makes itself, as `refs --rename` or a Buzz fs write, that no lease grades"
description: "A deny rule by default: it refuses a rewrite a magus process makes itself, as `refs --rename` or a Buzz fs write, that no lease grades, and names what to run instead."
tags: [guard, rules, edit-ungraded, deny]
---

# edit-ungraded

A deny rule by default: it refuses a rewrite a magus process makes itself, as `refs --rename` or a Buzz fs write, that no lease grades, and names what to run instead.

## What it catches

A rewrite a magus process makes itself, as `refs --rename` or a Buzz fs write, that no lease grades.

## Why

Such a process knows no host session, so it acts under its checkout's record, else the lease its magus.lease BAGGAGE names. A lease merely held in the checkout binds nobody, so a person's process stays unbound. The shell hook refuses a caller bound by its session a line that would rewrite with no claim, and the process itself refuses when the lease store does not read: either way nothing would grade the write, and passing it would make every lease boundary advice. Name the lease on the line: `export BAGGAGE=magus.lease=<id>; magus ...`.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"edit-ungraded": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [edit-ungraded]: ...
```

A workspace that sets this rule to `advise` sees `advise [edit-ungraded]: ...` instead.

`magus describe rule edit-ungraded` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
