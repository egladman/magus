---
title: "signpost"
description: "An advisory by default: it explains, and blocks nothing, on an announcement standing where the point should be (\"Here's the thing\", \"Let's dive in\")."
tags: [proofread, rules, signpost, advise]
aliases: [reference/prose/signpost]
---

# signpost

An advisory by default: it explains, and blocks nothing, on an announcement standing where the point should be ("Here's the thing", "Let's dive in").

## What it catches

An announcement standing where the point should be ("Here's the thing", "Let's dive in").

## Dimension

`structure`: the reader has to reconstruct the order or the purpose. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

A signpost delays the point it promises. It had zero hits in 261 docs pages, 665 changelog fragments and 200 merged pull requests. Measured 2026-10-10 before a fix: 85.7% precision on its cases (6 of 7), firing on 0.2% of AIDev pull requests and 0.1% of review comments; of 30 review-comment firings read, 23 were wrong: 6 "What if" real questions and 17 "Here's how/what/why ...:" lines captioning the code or list below. So "What if" counts only as "What if I told you", and "Here's" only before the thing, why it matters, or what it means. After: 100% on its cases; firing rates measured at merge.

## Default decision

The code is `PRF4003`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `reference`          | advise  |
| `principle`          | advise  |
| `guide`              | advise  |
| `change-description` | advise  |
| `agent-brief`        | advise  |
| `commit-message`     | advise  |
| `issue`              | advise  |
| `release-notes`      | advise  |
| `changelog`          | advise  |
| `review-summary`     | advise  |
| `review-inline`      | advise  |
| `review-reply`       | advise  |
| `agent-reply`        | advise  |
| `agent-compact`      | advise  |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"signpost": "deny"},
  "paths": {"blog/**": {"signpost": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "signpost", "code": "PRF4003", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/proofread/signpost/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
