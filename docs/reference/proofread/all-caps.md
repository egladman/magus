---
title: "all-caps"
description: "A deny rule by default: it refuses words in capitals for emphasis in a review reply (\"DO NOT\", \"NEVER\")."
tags: [proofread, rules, all-caps, advise]
aliases: [reference/prose/all-caps]
---

# all-caps

A deny rule by default: it refuses words in capitals for emphasis in a review reply ("DO NOT", "NEVER").

## What it catches

Words in capitals for emphasis in a review reply ("DO NOT", "NEVER").

## Dimension

`stance`: the text reads as a verdict on a person, or as addressed to someone else. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

Capitals read as shouting. An acronym is left alone: the rule reports a run of capital words only when it holds an English word such as NOT, NEVER or ALL. Measured 2026-10-10 over the AIDev review comments: 0.41 percent of 39639 written by people and 0.37 percent of 42076 written by bots, which capitalize ALL and ANY in technical prose too, so the rule does not tell the two apart and only advises.

## Default decision

The code is `PRF8013`. The decision depends on the kind of text judged:

| Kind           | Default |
| -------------- | ------- |
| `review-reply` | off     |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"all-caps": "deny"},
  "paths": {"blog/**": {"all-caps": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "all-caps", "code": "PRF8013", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/proofread/all-caps/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
