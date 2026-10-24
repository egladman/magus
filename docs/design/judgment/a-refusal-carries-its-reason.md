---
title: A refusal carries its reason
order: 5
description: A refusal names the mechanism and a next step; an error that leaves you with no next step is a design bug.
tags: [design, judgment, diagnostics]
---

# A refusal carries its reason

A refusal names the mechanism and a next step. Diagnostics are coded and
[documented](../../reference/diagnostics.md) so you can look one up. A stale binary
can surface as a typo and a missing tool as a lint finding; where magus can
tell the difference, the message says what is happening underneath. The same
rule reaches workflow refusals: `notes edit` declines to rewrite a note's
anchors from a pipe, and the error says why anchors are not pipeable and what
to do instead.

A denial that says no and nothing else sends you around the tool, and the
workaround runs outside the cache and the sandbox, where magus can no longer
account for the work. An error that leaves you with no next step is a design
bug; file it as one.

## Enforced by

- The `diagnostic-codes-have-docs-pages` lint fails a diagnostic code with no
  page under [the diagnostics reference](../../reference/diagnostics.md).
- A guard verdict carries `reason`, `rule` and `next`, the command to run
  instead.

## Good

A refusal from this repository's guard, naming why and what to run:

```text
`sed -i` reads differently on GNU and macOS; use your editor tool.
Renaming a symbol from a script? `./magus buzz hack/dev/rename-symbol.buzz -- --symbol <old> --to <new>`
```

## Bad

```text
error: operation not permitted
```

It names no rule and no next step, so the caller works around the tool, and
the workaround runs where magus can no longer account for it.

## See also

- [No silent defaults](no-silent-defaults.md)
- [Friction that carries meaning](../principles/friction-that-carries-meaning.md)
- [Diagnostics](../../reference/diagnostics.md)
