---
title: "stacked-hedge"
description: "An advisory by default: it explains, and blocks nothing, on two softeners in one sentence of a review reply, or an apology before its point."
tags: [proofread, rules, stacked-hedge, advise]
aliases: [reference/prose/stacked-hedge]
---

# stacked-hedge

An advisory by default: it explains, and blocks nothing, on two softeners in one sentence of a review reply, or an apology before its point.

## What it catches

Two softeners in one sentence of a review reply, or an apology before its point.

## Why

Stacked softeners read as unsure of a point the writer has. It advises: one tell proves nothing.

## Default decision

The code is `PRF8003`. The decision depends on the kind of text judged:

| Kind           | Default |
| -------------- | ------- |
| `review-reply` | advise  |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"stacked-hedge": "deny"},
  "paths": {"blog/**": {"stacked-hedge": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "stacked-hedge", "code": "PRF8003", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/proofread/stacked-hedge/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
