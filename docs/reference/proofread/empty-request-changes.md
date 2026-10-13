---
title: "empty-request-changes"
description: "A rule that is off by default: it reports a review requesting changes whose body names nothing blocking and points at no inline comment once a decisions table turns it on."
tags: [proofread, rules, empty-request-changes, off]
aliases: [reference/prose/empty-request-changes]
---

# empty-request-changes

A rule that is off by default: it reports a review requesting changes whose body names nothing blocking and points at no inline comment once a decisions table turns it on.

## What it catches

A review requesting changes whose body names nothing blocking and points at no inline comment.

## Dimension

`evidence`: a claim says more or less than what was shown. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

A review that requests changes holds the merge; GitHub's review API requires its body. Google's review guide (eng-practices, "The Standard of Code Review") asks the reviewer to say what blocks, and Ebert et al. (SANER 2019) found missing rationale the most frequent cause of confusion. A body naming code, a path, a line, a failure, a list of items or the inline comments passes. It needs the review's event (-event), and judges nothing without it. It is inferred from those sources rather than measured, so it advises.

## Default decision

The code is `PRF8032`. The decision depends on the kind of text judged:

| Kind             | Default |
| ---------------- | ------- |
| `review-summary` | off     |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"empty-request-changes": "deny"},
  "paths": {"blog/**": {"empty-request-changes": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "empty-request-changes", "code": "PRF8032", "decision": "off", "url": "https://eli.gladman.cc/magus/reference/proofread/empty-request-changes/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/README.md#findings) - the fields every finding carries
