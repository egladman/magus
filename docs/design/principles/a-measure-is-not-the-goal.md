---
title: A measure is not the goal
order: 36
description: A gate raises the floor and never certifies the ceiling; each gate says what it measures and what it cannot, every refusal teaches, and a gain is measured against a person's judgment before it is claimed.
tags: [design, principle, gates, measurement]
---

# A measure is not the goal

Making well-written code easy raises the floor. It does not gatekeep, because
the ceiling stays with the person: taste, naming, architecture and choosing the
right problem are theirs, and no gate here reaches them. magus is a
human-driven, purpose-built tool whose value is depth and what it refuses to
build, and slop is still possible in unskilled hands. A gate that forgets this
turns into four risks, and each has a stated answer.

- **Goodhart.** Once a gate is a number, tests get written to move it. Change
  risk is to be paired with mutation testing, which asks whether the tests
  check anything, so a test that runs a line and asserts nothing does not move
  the second number. Until that lands
  ([ADR 0023](../../decisions/0023-touched-functions-meet-change-risk-and-their-mutants.md)),
  such a test still lowers change risk, and change risk has no exception form.
- **False authority.** A green gate is not "good code". Each gate documents what
  it measures and what it cannot: coverage measures lines run, not behavior
  pinned; change risk measures complexity against coverage, not whether the
  design is right; lint measures a rule, not intent. None of them knows about
  taste, naming, architecture or whether the right problem is solved.
- **Deskilling.** A refusal that only says no teaches nothing. Every refusal
  names its reason and its fix
  ([A refusal carries its reason](../judgment/a-refusal-carries-its-reason.md)),
  so the person leaves knowing the rule.
- **Overclaiming.** A gain is measured, with precision and recall against a
  person's judgment, before it is claimed
  ([Measured, not asserted](measured-not-asserted.md)).

## Enforced by

- The change-risk gate judges each touched function against itself at base,
  and a function it cannot score is a finding that names why
  (`hack/lint/touched-functions-keep-change-risk-under-10.buzz`).
- A docs section on a gate's score, ceiling, floor or threshold that says what
  it measures also says what it does not
  (`hack/lint/gate-pages-say-what-they-do-not-measure.buzz`).
- A lint finding with no message or no fix is itself a finding
  (`hack/lint/lint-findings-carry-a-message-and-a-fix.buzz`).
- An unreleased change note that claims a measured gain links the run behind it
  (`hack/lint/measured-claims-cite-their-run.buzz`).

## Good

```text
parse.Parse has change risk 42.00 (complexity 6, coverage 0%), past 10 (12.00 at base)
Cover it with tests, or split it into functions that each score 10 or less.
```

The refusal names the number, the reason and the fix.

## Bad

```text
Coverage is 90%, so the code is well tested.
```

Coverage counts lines run. Tests that call the code and assert nothing reach
90% too.

## See also

- [Measured, not asserted](measured-not-asserted.md)
- [A refusal carries its reason](../judgment/a-refusal-carries-its-reason.md)
- [What we do not build is the product](what-we-do-not-build.md)
