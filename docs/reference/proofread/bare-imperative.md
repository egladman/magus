---
title: "bare-imperative"
description: "An advisory by default: it explains, and blocks nothing, on a short command in a review reply that gives no reason anywhere (\"Fix this.\")."
tags: [proofread, rules, bare-imperative, advise]
aliases: [reference/prose/bare-imperative]
---

# bare-imperative

An advisory by default: it explains, and blocks nothing, on a short command in a review reply that gives no reason anywhere ("Fix this.").

## What it catches

A short command in a review reply that gives no reason anywhere ("Fix this.").

## Dimension

`stance`: the text reads as a verdict on a person, or as addressed to someone else. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

Danescu-Niculescu-Mizil et al. (ACL 2013) found a bare imperative, and a request opening with "Please", read as less polite than one that gives its reason or asks. A command of five words or fewer counts, unless the reply gives a reason anywhere or the command names code or its target ("change this to appsettings"). Measured 2026-10-10 over the AIDev review comments, it fires on 4.56% of 39,639 replies written by people and 0.07% of 42,076 written by bots (firing rates, not precision). It advises: between people who share the context, a short command can be read as intended. Measured 2026-10-11 over the adjudicated labeling round: 37.2% precision (16 of 43) before, 100% (16 of 16) after. Most firings followed a sentence stating the problem ("Duplicate documentation. Remove it."), so only a reply made of bare commands is judged. Labeling round 3 measured 29 of 40, a lower bound of 0.57, which each graded finding weighs.

## Default decision

The code is `PRF8012`. The decision depends on the kind of text judged:

| Kind             | Default |
| ---------------- | ------- |
| `review-summary` | advise  |
| `review-inline`  | advise  |
| `review-reply`   | advise  |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"bare-imperative": "deny"},
  "paths": {"blog/**": {"bare-imperative": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "bare-imperative", "code": "PRF8012", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/proofread/bare-imperative/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
