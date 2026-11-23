---
title: Measures in, verdicts out
order: 43
description: magus computes measures; a repository's own analyzers turn them into verdicts, under policy that repository types. No pass or fail is compiled into magus.
tags: [design, principle, linters, conformance, policy]
---

# Measures in, verdicts out

magus measures. It builds the graph, counts each conformance metric against an
anchor, mines the precedents the code already follows, and measures the base
revision beside the head. A measure says what the code is. It never says
whether a change may merge.

A verdict is the repository's. Its analyzers read the measures, read the
`analyzers.yaml` of the project that owns the file, and return a finding with
a message and a fix. Which metric fails a change, which only advises, and
which items a person excused all live in files the repository owns.

The line falls there because the measure is general and the verdict is local.
Every workspace wants file length measured the same way. Whether 1,600 lines
is debt, a decision or a refactor in progress depends on the code, so a
threshold compiled into the binary would be wrong somewhere. It would also be
a verdict nobody in the repository can read or change.

- magus ships default anchors, which decide what counts as past a line. Whether
  past the line fails is the lint target's call.
- `magus doctor`'s conformance check advises and never fails: it reports
  anchors the code measures tighter than, so a person can lower them.
- An unknown measure is no verdict either way: a rule that gets an unknown
  answer reports that it could not judge.

## Enforced by

- `hack/lint/code-stays-within-the-conformance-ceiling.buzz` judges with the
  policy in `magusfiles/quality/conformance-ceiling.buzz`; magus supplies only the
  head and base measures.
- Every rule names its tier: mechanical and measured rules fail, judgment
  rules advise (`hack/lint/rules-name-their-tier.buzz`).
- The Go analyzers take their paths, word lists and ceilings from config, never
  from code ([the conventions README](https://github.com/egladman/magus/blob/main/libs/conventions/README.md)).
- Nothing refuses a verdict added to magus core yet, so the split holds in
  review alone.

## Good

```text
magus\change().conformance()   -> rows: file-lines, func-lines, each item past its anchor
conformance-ceiling.buzz       -> a touched file holds more func-lines violations than at base: fail
```

magus counts; the repository's Buzz decides which count fails.

## Bad

```text
$ magus run lint
magus: parse.go fails func-lines: 3 functions over 167 lines
```

A refusal compiled into magus, keyed to a number the repository never chose and
cannot excuse.

## See also

- [A file a person edits is YAML](a-file-a-person-edits-is-yaml.md)
- [A measure is not the goal](a-measure-is-not-the-goal.md)
- [How the linters work](../../concepts/linters.md)
