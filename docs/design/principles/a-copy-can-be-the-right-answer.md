---
title: A copy can be the right answer
order: 12
description: Merge duplicates only when they share semantics, knobs and bugs; otherwise keep the copy, with one line saying why.
tags: [design, principle, code]
---

# A copy can be the right answer

Merge duplicates only when they share semantics, policy knobs, and bugs: a bug
in one is a bug in the other. Otherwise keep them apart, with one line saying
why. Share small things (a type, a classification) before sharing loops.

Code shared only because it looks alike couples parts that change for
different reasons. That coupling grows until one change breaks something far
away. This prevents the abstraction that is cheap to write and costly to
change, and it is why a deliberate copy carries its reason beside it.

## Enforced by

- Review. The `duplication` lens of `magus\insight()` finds functions that call
  the same symbols in the same proportions; it reports and never decides.

## Good

A copy with its reason beside it, from [`docs/lib/design.buzz`](https://github.com/egladman/magus/blob/main/docs/lib/design.buzz):

```buzz
// A design page's title is plain text; a quoted one loses only its quotes, since the
// conventions check that would unescape it imports this file.
fun unquoted(v: str) > str {
```

## Bad

```go
// parseConfig handles both magus.yaml and the job store.
func parseConfig(path string, strict, jsonLines, allowUnknown bool) (any, error)
```

Two formats with different rules now share one function and four flags. A fix
for the job store changes how `magus.yaml` reads, and every caller has to know
which flags its format needs.

## See also

- [Behavior is code, facts are data](behavior-is-code-facts-are-data.md)
- [Capability is not a reason](capability-is-not-a-reason.md)
