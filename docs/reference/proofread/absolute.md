---
title: "absolute"
description: "An advisory by default: it explains, and blocks nothing, on never, nobody or nothing as a claim about the past (\"has never fired\", \"nobody checked\")."
tags: [proofread, rules, absolute, advise]
aliases: [reference/prose/absolute]
---

# absolute

An advisory by default: it explains, and blocks nothing, on never, nobody or nothing as a claim about the past ("has never fired", "nobody checked").

## What it catches

Never, nobody or nothing as a claim about the past ("has never fired", "nobody checked").

## Dimension

`evidence`: a claim says more or less than what was shown. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

An absolute over every run since a change reads as a verdict on whoever made it; saying when and how often states the same fact. It advises because this repository's docs use "never" 788 times and every sampled use states a contract ("never returns nil").

## Default decision

The code is `PRF2003`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `change-description` | advise  |
| `review-reply`       | advise  |
| `commit-message`     | advise  |
| `issue`              | advise  |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"absolute": "deny"},
  "paths": {"blog/**": {"absolute": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "absolute", "code": "PRF2003", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/proofread/absolute/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
