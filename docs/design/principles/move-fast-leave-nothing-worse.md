---
title: Move fast, but leave nothing worse where it costs
order: 42
description: Move as fast as you like inside a change, but a change may not make the codebase measurably worse where it is expensive, nor add debt nobody signed for.
tags: [design, principle, gates, debt]
---

# Move fast, but leave nothing worse where it costs

Writing code is cheap now, so the question is how much slop a fast change may
carry. This repository's answer: move as fast as you like inside a change, but
a change may not make the codebase measurably worse where it is expensive, nor
add debt nobody signed for. Past debt is tolerated. New debt is refused unless
a person types it in with a reason.

How strict a rule is depends on what the slop costs to carry. A clumsy line in
a leaf function costs one reader one minute, and the next change can fix it. A
bad name in a public API, a record format or stored state costs every caller
and every file on disk, and after 1.0 it cannot be fixed at all
([Break now, hold still later](break-now-hold-still-later.md)). So the gates
are loose on code nobody touches and tight where slop outlives the change.

The limits come from this repository's own numbers, not from a book. Each size
anchor is a percentile of the tree as it stands, and only code a change
touches pays. Deletion is the counterweight: shrinking a function or removing
covered code never costs ([Delete before you add](delete-before-you-add.md)).

A measurement that shows a limit refusing good changes, or a kind of debt that
is cheap to carry after all, reopens this page.

## Enforced by

- A baseline may only shrink. The `no-throw` rule
  (`hack/lint/no-throw.buzz`), the `accessorclone` analyzer
  (`libs/conventions/accessorclone`) and the `closed-switches-name-every-member`
  rule read their `baseline` from `analyzers.yaml`; a count above the real one
  fails, and a new violation fails at once.
- A changed file may not hold more conformance violations than it held at base,
  and an item already past its anchor may not grow
  (`hack/lint/code-stays-within-the-conformance-ceiling.buzz`, anchors under
  `knowledge.conformance.checks` in `magus.yaml`).
- A touched function may not score a change risk above 10 and worse than at
  base; an untouched one is never charged
  (`hack/lint/touched-functions-keep-change-risk-under-10.buzz`,
  [ADR 0023](../../decisions/0023-touched-functions-meet-change-risk-and-their-mutants.md)).
- The expensive kinds have their own rules. `hack/lint/sdk-exports.buzz` makes
  every SDK export carry a reason in `hack/lint/sdk-exports.yaml`; the
  `jsonlstate` analyzer holds stored state to JSON Lines; `libs/apiversion`
  makes a record reader refuse a header but its own; the `breaking` target in
  `proto/magusfile.buzz` runs buf-breaking.
- An exception is visible and owned. Exits in `analyzers.yaml` carry a reason,
  and the job's write paths never list that file, so the guard denies an
  agent's edit to it. `accessorclone` and `closed-switches-name-every-member`
  refuse a `//nolint` naming them. A finding a change means to cause is
  accepted in a ledger with a reason
  ([ADR 0021](../../decisions/0021-a-change-accepts-the-findings-it-causes.md)).
- `magus describe job` reports a job's net lines and the symbols it left with
  no use (`internal/job/plan.go`).

Not enforced yet, and so a debt in [Where this is strained](../strained.md):
the `zones` in `analyzers.yaml` are declared but no rule reads them; limits do
not yet weigh churn; no autofix takes the cheap kind of slop; no per-directory
budget nets additions against deletions; and the study that ranks length and
churn above cyclomatic complexity is not committed, so that ranking is
experience.

## Good

An exit a person typed, with the reason beside it, from `analyzers.yaml`:

```yaml
no-throw:
  exits:
    - path: spells/harness/opencode/plugins/magus.js
      scope: MagusGuard
      reason: a throw from the plugin entry is OpenCode's only way to refuse to load the guard
```

## Bad

```go
switch kind { //nolint:closedswitch // will fix later
default:
    return nil
}
```

Nobody signed for this debt and nothing says when it ends. The next member
added to `kind` takes the default arm in silence.

## See also

- [Measured, not asserted](measured-not-asserted.md)
- [A measure is not the goal](a-measure-is-not-the-goal.md)
- [Enforcement, not pinky promises](enforcement-not-pinky-promises.md)
- [A deliberate break never merges red](a-deliberate-break-never-merges-red.md)
