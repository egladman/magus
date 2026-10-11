---
title: "nonspecific"
description: "An advisory by default: it explains, and blocks nothing, on a review reply that judges or asks for a change and names no code, path, line, example or reason."
tags: [proofread, rules, nonspecific, advise]
aliases: [reference/prose/nonspecific]
---

# nonspecific

An advisory by default: it explains, and blocks nothing, on a review reply that judges or asks for a change and names no code, path, line, example or reason.

## What it catches

A review reply that judges or asks for a change and names no code, path, line, example or reason.

## Dimension

`evidence`: a claim says more or less than what was shown. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

Gunawardena et al. (CSCW 2022) define destructive criticism as feedback that is nonspecific and inconsiderate, and over half of their respondents had received it in the past year; Bosu et al. (MSR 2015) found a third of review comments were not useful. Measured 2026-10-10 over the AIDev review comments: 2.92 percent of 39639 written by people and 0.15 percent of 42076 written by bots. It advises: an inline comment already sits on its line, which the rule cannot see.

## Default decision

The code is `PRF8010`. The decision depends on the kind of text judged:

| Kind           | Default |
| -------------- | ------- |
| `review-reply` | advise  |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"nonspecific": "deny"},
  "paths": {"blog/**": {"nonspecific": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "nonspecific", "code": "PRF8010", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/proofread/nonspecific/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
