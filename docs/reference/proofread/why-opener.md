---
title: "why-opener"
description: "An advisory by default: it explains, and blocks nothing, on a review reply sentence that opens \"Why did you\" or \"Why would you\"."
tags: [proofread, rules, why-opener, advise]
aliases: [reference/prose/why-opener]
---

# why-opener

An advisory by default: it explains, and blocks nothing, on a review reply sentence that opens "Why did you" or "Why would you".

## What it catches

A review reply sentence that opens "Why did you" or "Why would you".

## Dimension

`stance`: the text reads as a verdict on a person, or as addressed to someone else. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

Danescu-Niculescu-Mizil et al. (ACL 2013) found a direct question opening with "why" among the strongest cues of an impolite request: it asks the author to defend themselves. Asking what the code needs ("Does this need the lock?") asks the same. Measured 2026-10-10 over the AIDev review comments: 0.16 percent of 39639 written by people and none of 42076 written by bots. It advises: the author may want the reason on record.

## Default decision

The code is `PRF8011`. The decision depends on the kind of text judged:

| Kind           | Default |
| -------------- | ------- |
| `review-reply` | advise  |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"why-opener": "deny"},
  "paths": {"blog/**": {"why-opener": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "why-opener", "code": "PRF8011", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/proofread/why-opener/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
