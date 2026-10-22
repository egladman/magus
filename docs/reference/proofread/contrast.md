---
title: "contrast"
description: "A rule that is off by default: it reports a claim made by denying its opposite first (\"not just X, it is Y\") once a decisions table turns it on."
tags: [proofread, rules, contrast, off]
aliases: [reference/prose/contrast]
---

# contrast

A rule that is off by default: it reports a claim made by denying its opposite first ("not just X, it is Y") once a decisions table turns it on.

## What it catches

A claim made by denying its opposite first ("not just X, it is Y").

## Dimension

`economy`: words that carry nothing. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

Negative parallelism argues with a position nobody took. It denies in a change description, where 200 merged pull requests used it 0 times, and advises elsewhere, where the docs use it deliberately 15 times. Measured 2026-10-11 over the adjudicated labeling round: 61.2% precision (30 of 49) before, 100% (30 of 30, lower bound 0.89) after, so it ships advise everywhere. Left alone now: quoted words, a conditional ("if it is not provided, it is derived"), a correction in parentheses, "not only" before anything but an article, a contrast answering a position the paragraph reports ("You proposed ..."), and "no X, no Y, just" listing what happened.

## Default decision

The code is `PRF4010`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `reference`          | off     |
| `guide`              | off     |
| `change-description` | off     |
| `agent-brief`        | off     |
| `commit-message`     | off     |
| `issue`              | off     |
| `release-notes`      | off     |
| `changelog`          | off     |
| `review-summary`     | off     |
| `review-inline`      | off     |
| `review-reply`       | off     |
| `agent-compact`      | off     |
| `agent-reply`        | off     |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"contrast": "deny"},
  "paths": {"blog/**": {"contrast": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "contrast", "code": "PRF4010", "decision": "off", "url": "https://eli.gladman.cc/magus/reference/proofread/contrast/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
