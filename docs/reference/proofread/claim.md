---
title: "claim"
description: "An advisory by default: it explains, and blocks nothing, on a measurement, a comparison or a completion with no evidence in its sentence or bullet."
tags: [proofread, rules, claim, advise]
aliases: [reference/prose/claim]
---

# claim

An advisory by default: it explains, and blocks nothing, on a measurement, a comparison or a completion with no evidence in its sentence or bullet.

## What it catches

A measurement, a comparison or a completion with no evidence in its sentence or bullet.

## Why

A claim a reader cannot check reads as boasting or as a guess. Evidence is a code span naming a test or command, a link, an issue or commit, or an output ref. It advises: over the last 200 merged pull requests it fired in 21, and about half of those state a setting ("a 300s bound") rather than measure. A sentence that states a limit ("Not measured on Linux") is exempt.

## Default decision

The code is `PRF3001`. The decision depends on the kind of text judged:

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
  "rules": {"claim": "deny"},
  "paths": {"blog/**": {"claim": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "claim", "code": "PRF3001", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/proofread/claim/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
