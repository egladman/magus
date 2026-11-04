---
title: A deliberate break never merges red
order: 34
description: A person making a deliberate break is never put in the position of merging a red pull request; the change carries its own recorded way to green.
tags: [design, principle, compatibility, gates]
---

# A deliberate break never merges red

"I should never be put in a scenario where I am expected to merge something that
is red." A person making a deliberate break is owed a green pull request, and the
change carries its own recorded way to get there.

A break is a choice made with discretion. Being before 1.0 means a break may be
made, as [Break now, hold still later](break-now-hold-still-later.md) says. It does
not make breaking the norm, and a gate that catches a break stays on so that an
accidental one still fails.

So the gate keeps comparing, and the way through is a line in the same diff: which
finding this change permits, and why. Merging past a red check is not a way through,
and neither is switching the gate off for everyone after this change.

## Enforced by

- A comparison gate reads `<project>/<gate>.deviations.jsonl`, one `types.Deviation`
  per line. Only a line this change added counts, so the next change answers for
  what it did. `proto` `ci` is the first gate to use it.
- A finding with no line fails and prints the line to add. A line with no finding
  fails and names the line to delete. A blank reason is an error.
- The verdict reads only the tree and the base, so CI sees the same bytes whoever
  wrote them. The `deviation-author` guard rule, a seatbelt rather than a boundary,
  refuses an agent adding a line.

- `pr-deliberate-break`, run by `.github/workflows/deliberate-break.yaml` on
  `pull_request_target`, lists each line a pull request adds and each edit to the gate
  files, as a job summary and a notice per line. It reads the changed files through the
  API and never runs the pull request's code. It always succeeds: a person's merge is the
  check on the list.
- A ledger that is a symlink, or whose name does not end in `.deviations.jsonl`, is an error.

## Good

The change that removes the value carries the decision, and `ci` stays green:

```json
{"code":"ENUM_VALUE_NO_DELETE_UNLESS_NAME_RESERVED","subject":"magus/activity/v1alpha1/activity.proto: Previously present enum value \"6\" on enum \"Kind\" was deleted without reserving the name \"KIND_MEMORY\".","reason":"the memory store is gone; an older console bundle reloads on the version mismatch"}
```

## Bad

```yaml
breaking:
  use: [WIRE_JSON]
  ignore_only:
    WIRE_JSON: [magus/activity/v1alpha1/activity.proto]
```

The check is green because it stopped looking. The waiver covers every later break
in that file, and it stays until someone remembers to remove it.

## See also

- [Break now, hold still later](break-now-hold-still-later.md)
- [Verdicts are provenance-blind](verdicts-are-provenance-blind.md)
- [ADR 0021](../../decisions/0021-a-change-accepts-the-findings-it-causes.md)
