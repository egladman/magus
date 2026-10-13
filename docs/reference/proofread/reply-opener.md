---
title: "reply-opener"
description: "A rule that is off by default: it reports a sentence of a review reply that opens by contradicting (\"No,\", \"As I said\") once a decisions table turns it on."
tags: [proofread, rules, reply-opener, off]
aliases: [reference/prose/reply-opener]
---

# reply-opener

A rule that is off by default: it reports a sentence of a review reply that opens by contradicting ("No,", "As I said") once a decisions table turns it on.

## What it catches

A sentence of a review reply that opens by contradicting ("No,", "As I said").

## Dimension

`stance`: the text reads as a verdict on a person, or as addressed to someone else. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

A contradiction first reads as winning an argument whatever follows it. Measured 2026-10-11 over the adjudicated labeling round: 44.2% precision (19 of 43) before, 95.0% (19 of 20) after. "Wrong" counts only as an exclamation, not before a noun ("Wrong indentation"), and a "No," answering the question the reply quotes, or conceding ("good catch"), is left alone. It stays off until 30 labeled firings show it.

## Default decision

The code is `PRF8001`. The decision depends on the kind of text judged:

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
  "rules": {"reply-opener": "deny"},
  "paths": {"blog/**": {"reply-opener": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "reply-opener", "code": "PRF8001", "decision": "off", "url": "https://eli.gladman.cc/magus/reference/proofread/reply-opener/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/README.md#findings) - the fields every finding carries
