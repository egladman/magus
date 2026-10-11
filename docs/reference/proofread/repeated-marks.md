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

## Why

Repeated marks read as exasperation where one mark asks the same question. Measured 2026-10-10 over the AIDev review comments: 0.17 percent of 39639 written by people and 0.05 percent of 42076 written by bots. Code spans are masked, so an operator such as ?? passes.

## Default decision

The code is `PRF8014`. The decision depends on the kind of text judged:

| Kind           | Default |
| -------------- | ------- |
| `review-reply` | advise  |

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
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
