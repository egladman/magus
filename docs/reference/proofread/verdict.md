---
title: "verdict"
description: "A deny rule by default: it refuses a judgment word standing in for the behavior it judges (\"was broken\", \"a mess\")."
tags: [proofread, rules, verdict, advise]
aliases: [reference/prose/verdict]
---

# verdict

A deny rule by default: it refuses a judgment word standing in for the behavior it judges ("was broken", "a mess").

## What it catches

A judgment word standing in for the behavior it judges ("was broken", "a mess").

## Dimension

`stance`: the text reads as a verdict on a person, or as addressed to someone else. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

A verdict tells the reader what to think in place of what happened. It advises because "broken" and "wrong" also name a broken test or the wrong checkout; it moves to deny only after every firing on real text was right.

## Default decision

The code is `PRF2002`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `change-description` | off     |
| `review-reply`       | off     |
| `commit-message`     | off     |
| `issue`              | off     |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"verdict": "deny"},
  "paths": {"blog/**": {"verdict": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "verdict", "code": "PRF2002", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/proofread/verdict/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
