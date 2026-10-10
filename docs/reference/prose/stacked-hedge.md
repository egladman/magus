---
title: "stacked-hedge"
description: "An advisory by default: it explains, and blocks nothing, on two softeners in one sentence of a review reply, or an apology before its point."
tags: [prose, rules, stacked-hedge, advise]
---

# stacked-hedge

An advisory by default: it explains, and blocks nothing, on two softeners in one sentence of a review reply, or an apology before its point.

## What it catches

Two softeners in one sentence of a review reply, or an apology before its point.

## Why

Stacked softeners read as unsure of a point the writer has. It advises: one tell proves nothing.

## Default decision

The code is `PRS8003`. The decision depends on the kind of text judged:

| Kind           | Default |
| -------------- | ------- |
| `review-reply` | advise  |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. The prose judge reads the table from the file its
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
{"rule": "stacked-hedge", "code": "PRS8003", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/prose/stacked-hedge/"}
```

The judge's `-catalog` flag prints this entry with every other rule's.

## See also

- [All prose rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
