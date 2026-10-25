---
title: "repeated-marks"
description: "An advisory by default: it explains, and blocks nothing, on a run of question or exclamation marks in a review reply (\"??\", \"!!\", \"?!\")."
tags: [proofread, rules, repeated-marks, advise]
aliases: [reference/prose/repeated-marks]
---

# repeated-marks

An advisory by default: it explains, and blocks nothing, on a run of question or exclamation marks in a review reply ("??", "!!", "?!").

## What it catches

A run of question or exclamation marks in a review reply ("??", "!!", "?!").

## Dimension

`stance`: the text reads as a verdict on a person, or as addressed to someone else. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

Repeated marks read as exasperation where one mark asks the same question. Measured 2026-10-10 over the AIDev review comments, it fires on 0.17% of 39,639 replies written by people and 0.05% of 42,076 written by bots (firing rates, not precision). Code spans are masked, so an operator such as ?? passes. Measured 2026-10-11 over the adjudicated labeling round: 46.5% precision (20 of 43) before, 100% (20 of 20) after: every false positive was an operator or placeholder outside a code span, so a run set between spaces or brackets, or touching a word it opens ("!!hit", "!!NAME!!"), is left alone. Labeling round 3 measured 34 of 34, a lower bound of 0.90, which each graded finding weighs.

## Default decision

The code is `PRF8014`. The decision depends on the kind of text judged:

| Kind             | Default |
| ---------------- | ------- |
| `review-summary` | advise  |
| `review-inline`  | advise  |
| `review-reply`   | advise  |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"repeated-marks": "deny"},
  "paths": {"blog/**": {"repeated-marks": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "repeated-marks", "code": "PRF8014", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/proofread/repeated-marks/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
