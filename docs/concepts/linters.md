---
title: How the linters work
description: "magus measures; this repository's analyzers judge; each project's analyzers.yaml configures them. What decides a finding, in order, and how the repository holds its own code to its own rules."
tags: [linters, analyzers, conformance, baseline, policy, lint]
---

# How the linters work

Three parts, each with one job:

- **magus measures.** The knowledge graph counts each conformance metric
  against an anchor, mines the precedents the code already follows, and
  measures the base revision beside the head
  ([Measures in, verdicts out](../design/principles/measures-in-verdicts-out.md)).
- **The analyzers judge.** The Go analyzers in `libs/conventions` and
  `libs/testpairing` run inside golangci-lint. The Buzz rules in `hack/lint`
  run through `hack/lint/run.buzz`, and the conformance ceiling policy is
  `magusfiles/quality/conformance-ceiling.buzz`.
- **`analyzers.yaml` configures them.** Each project root holds its own. An
  analyzer judges a file by the file of the project that owns it, and a project
  with none gets the strict defaults
  ([A file a person edits is YAML](../design/principles/a-file-a-person-edits-is-yaml.md)).

<!--diagram:linters-loop-->

## The boundary

The measures cross into Buzz through the host modules, as
`magus\change().conformance()`. The Go analyzers import no magus package: they
read source and their own `analyzers.yaml`, and nothing else.

<!--diagram:linters-boundary-->

## What decides a finding

A check asks these in order:

1. **Is it a verdict at all?** A measure alone never fails a run. A rule's
   header names its tier: a mechanical or measured rule fails, a judgment rule
   prints advice ([CONTRIBUTING](https://github.com/egladman/magus/blob/main/CONTRIBUTING.md#rolling-out-a-stricter-rule)).
2. **Is an item past its anchor growing?** An item already past its anchor at
   the base revision may not move further past it, in a zone or out of one. No
   baseline, exit or zone lifts this.
3. **Did the change add to the file?** A file the change touches may not hold
   more violations of a checked metric than it held at the base. A change
   answers only for what it did.
4. **Did a person excuse it?** An `exits` entry excuses one item for good, and
   carries a required `reason`.
5. **Is it debt the rule landed with?** A `baseline` lists today's violations.
   It may only shrink: a stale entry fails, and `magus run format .` drops the
   entries the code no longer needs.
6. **Does its path sit in a zone?** A zone swaps the strict budgets for its
   own, licensed by `evidence` and a `reason` (`spec`, `performance` or
   `security`). No rule reads zones yet, so the strict budgets apply everywhere.
7. **Otherwise, the strict default.** Nothing is excused.

An analyzer whose policy a person types (`accessorclone`, `closedswitch`,
`no-throw`) takes no inline waiver: a marker comment excuses nothing, and a
`//nolint` naming `accessorclone` or `closedswitch` is itself a finding.

## The retry loop

Each finding prints as `path:line:col: message [rule]` with its fix indented
below, and a finding with no message or no fix is itself a finding
(`hack/lint/lint-findings-carry-a-message-and-a-fix.buzz`). A cheap model in a
loop needs nothing else: it applies the fix and reruns the scoped
`magus run lint -- <files>`.

The model cannot widen the policy instead. A job never lists `analyzers.yaml`
in its write paths, so the guard denies an agent's edit to it, and the
`accessorclone` hint says that only a person adds a baseline entry. The
loop ends when the code is fixed or a person decides.

## How the repository improves itself

magus builds itself, and its lint target holds magus's own source to these
rules on every pull request.

- Precedents are mined from this tree, and `code-follows-mined-precedents`
  fails a departure from one. A kept departure is listed with why, and an entry
  that no longer departs fails, so the list only shrinks.
- Baselines only shrink, so every fix lowers the floor for the next change.
- `magus doctor` reports anchors the code now measures tighter than, so a person
  can lower them.
- The rules are held to rules: each names its tier
  (`rules-name-their-tier`), and each finding carries a message and a fix.
- A rule's author pays its cost: a mechanical rule lands with its sweep, a
  measured one with a baseline.

## See also

- [libs/conventions README](https://github.com/egladman/magus/blob/main/libs/conventions/README.md): every Go analyzer and what it reports.
- [libs/testpairing README](https://github.com/egladman/magus/blob/main/libs/testpairing/README.md): the test-layout analyzers.
- [A measure is not the goal](../design/principles/a-measure-is-not-the-goal.md)
- [ADR 0053](../decisions/0053-an-op-declares-the-files-it-edits-in-place.md): a fixer such as `go::golangci-lint:rw` declares the files it rewrites; the check variant still fails on drift.
