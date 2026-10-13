---
title: "condescension"
description: "A rule that is off by default: it reports a word that tells the reader how hard a step should feel or what they should already know once a decisions table turns it on."
tags: [proofread, rules, condescension, advise]
aliases: [reference/prose/condescension]
---

# condescension

A rule that is off by default: it reports a word that tells the reader how hard a step should feel or what they should already know once a decisions table turns it on.

## What it catches

A word that tells the reader how hard a step should feel or what they should already know.

## Dimension

`stance`: the text reads as a verdict on a person, or as addressed to someone else. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

"Simply run" and "of course" tell a reader who is stuck that they should not be. Measured over the guides before the rule, none of 16 lowercase "just" minimized a step, so "just" counts only before a verb the reader carries out, and an "easy" that warns ("easy to get wrong") is left alone. Measured 2026-10-11 over the adjudicated labeling round: 44.7% precision (21 of 47) before, 91.3% (21 of 23) after. "Clearly" before a verb says how ("Clearly marks the flag"), so it counts only before a comma, "not" or a subject; quoted words, an "obviously" about the writer's own doing and "Of course," assenting in a reply are left alone. It stays off until 30 labeled firings show it.

## Default decision

The code is `PRF2006`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `reference`          | off     |
| `guide`              | off     |
| `change-description` | off     |
| `agent-instructions` | off     |
| `commit-message`     | off     |
| `issue`              | off     |
| `release-notes`      | off     |
| `changelog`          | off     |
| `review-summary`     | off     |
| `review-inline`      | off     |
| `review-reply`       | off     |
| `cli-help`           | off     |
| `agent-reply`        | off     |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"condescension": "deny"},
  "paths": {"blog/**": {"condescension": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "condescension", "code": "PRF2006", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/proofread/condescension/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/README.md#findings) - the fields every finding carries
