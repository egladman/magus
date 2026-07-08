---
title: race diagnostics
page_type: overview
description: Landing page for MGS4xxx race condition diagnostics emitted by the magus race detector across static, watch, and replay modes.
tags:
  [
    race,
    diagnostics,
    error codes,
    MGS4xxx,
    concurrency,
    determinism,
    watch,
    replay,
  ]
---

# Race condition diagnostics

Codes in the `MGS4xxx` range are emitted by the magus race condition detector.
Enable with `magus run <target> --race`.

The `--race` flag follows the `--output` pattern: an enumerated mode value.
Modes are **orthogonal** and can be combined with a comma:

```text
magus run build                          # no race diagnostics
magus run build --race=watch             # cheap: fsnotify + static checks
magus run build --race=replay            # determinism only
magus run build --race=watch,replay      # everything
```

| Mode           | Codes emitted                   | Cost                           |
| -------------- | ------------------------------- | ------------------------------ |
| (omitted)      | `MGS4002` only                  | free (static check, always on) |
| `watch`        | `MGS4001`, `MGS4002`, `MGS4004` | near-zero (fsnotify)           |
| `replay`       | `MGS4002`, `MGS4003`            | roughly doubles wall-clock     |
| `watch,replay` | all four                        | watch overhead + 2× wall-clock |

`MGS4002` (declared-output overlap) is always emitted: a static check
at graph construction time, with zero runtime cost, no flag required.

`watch` is safe to leave on for every CI run. `replay` re-executes the
affected set sequentially with the cache bypassed, so reserve it for nightly CI
or a manual audit rather than every push.

## Codes

- [MGS4001](MGS4001.md): filesystem race condition.
- [MGS4002](MGS4002.md): declared output overlap.
- [MGS4003](MGS4003.md): non-deterministic output.
- [MGS4004](MGS4004.md): potential undeclared dependency.
