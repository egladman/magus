---
title: "bare-imperative"
description: "A deny rule by default: it refuses a short command in a review reply that gives no reason anywhere (\"Fix this.\")."
tags: [proofread, rules, bare-imperative, advise]
aliases: [reference/prose/bare-imperative]
---

# bare-imperative

A deny rule by default: it refuses a short command in a review reply that gives no reason anywhere ("Fix this.").

## What it catches

A short command in a review reply that gives no reason anywhere ("Fix this.").

## Dimension

`stance`: the text reads as a verdict on a person, or as addressed to someone else. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

Danescu-Niculescu-Mizil et al. (ACL 2013) found a bare imperative, and a request opening with "Please", read as less polite than one that gives its reason or asks. A command of five words or fewer counts, unless the reply gives a reason anywhere or the command names code. Measured 2026-10-10 over the AIDev review comments: 4.56 percent of 39639 written by people and 0.07 percent of 42076 written by bots. It advises: between teammates who share the context, a short command can be read as intended.

## Default decision

The code is `PRF8012`. The decision depends on the kind of text judged:

| Kind           | Default |
| -------------- | ------- |
| `review-reply` | off     |

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
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
