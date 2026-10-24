---
title: "grade"
description: "A deny rule by default: it refuses a text whose grade, out of 100, is under the minimum its kind is held to."
tags: [proofread, rules, grade, deny]
aliases: [reference/prose/grade]
---

# grade

A deny rule by default: it refuses a text whose grade, out of 100, is under the minimum its kind is held to.

## What it catches

A text whose grade, out of 100, is under the minimum its kind is held to.

## Dimension

`structure`: the reader has to reconstruct the order or the purpose. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

An advise finding fails nothing alone, so twenty of them read the same as one. The grade is 100 less 100 times the summed weight of a text's advise findings over 1 + words/100, words capped at 1000, floored at 0. A finding weighs its rule's Wilson 95% lower bound on precision in its latest labeling round, so a rule right 4 times in 5 costs four times one right 1 time in 5, and a rule no round measured costs nothing. A deny finding fails a text whatever its grade. The minimum is the caller's policy, set per kind in the decisions table; proofread ships none.

## Default decision

The code is `PRF1100`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `doc-comment`        | deny    |
| `reference`          | deny    |
| `principle`          | deny    |
| `change-description` | deny    |
| `agent-brief`        | deny    |
| `agent-compact`      | deny    |
| `skill-template`     | deny    |
| `guide`              | deny    |
| `review-summary`     | deny    |
| `review-inline`      | deny    |
| `review-reply`       | deny    |
| `commit-message`     | deny    |
| `cli-help`           | deny    |
| `issue`              | deny    |
| `release-notes`      | deny    |
| `changelog`          | deny    |
| `agent-reply`        | deny    |
| `tool-description`   | deny    |
| `stylesheet`         | deny    |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"grade": "advise"},
  "paths": {"blog/**": {"grade": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "grade", "code": "PRF1100", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/grade/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
