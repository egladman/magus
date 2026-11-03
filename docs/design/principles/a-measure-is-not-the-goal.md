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
  risk is paired with mutation testing, which asks whether the tests check
  anything, so a test that runs a line and asserts nothing does not move the
  second number. The one exception marker needs a written reason.
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

- The mutation and change-risk gates run together, and an exception marker
  without a reason is itself a finding.
- A gate's reference page carries a "measures / does not measure" pair.
- A refusal with no reason or no fix fails the refusal-shape check.
- A claim of improvement links the precision and recall run behind it.

## Good

```text
change risk 41 on parse.go: mutation score 0.30, 7 of 10 mutants survived;
assert on the returned error in TestParseRejectsEmpty, or delete the branch
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
