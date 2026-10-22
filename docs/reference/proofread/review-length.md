---
title: "review-length"
description: "A rule that is off by default: it reports a review text over its word cap: an inline comment over 60 words, a summary over 100, a reply over 80 once a decisions table turns it on."
tags: [proofread, rules, review-length, off]
aliases: [reference/prose/review-length]
---

# review-length

A rule that is off by default: it reports a review text over its word cap: an inline comment over 60 words, a summary over 100, a reply over 80 once a decisions table turns it on.

## What it catches

A review text over its word cap: an inline comment over 60 words, a summary over 100, a reply over 80.

## Dimension

`economy`: words that carry nothing. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

A convention, not yet measured: the caps are the author's own sense of a comment a reader takes in at a glance, and no labeled round has checked them against comments that were acted on. A long comment can be right, so it advises, and ships off until a round weighs it.

## Default decision

The code is `PRF8038`. The decision depends on the kind of text judged:

| Kind             | Default |
| ---------------- | ------- |
| `review-summary` | off     |
| `review-inline`  | off     |
| `review-reply`   | off     |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"review-length": "deny"},
  "paths": {"blog/**": {"review-length": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "review-length", "code": "PRF8038", "decision": "off", "url": "https://eli.gladman.cc/magus/reference/proofread/review-length/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
