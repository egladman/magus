---
title: "intent"
description: "A rule that is off by default: it reports a motive given to a tool or a person (\"guessed\", \"pretends\") once a decisions table turns it on."
tags: [proofread, rules, intent, advise]
aliases: [reference/prose/intent]
---

# intent

A rule that is off by default: it reports a motive given to a tool or a person ("guessed", "pretends") once a decisions table turns it on.

## What it catches

A motive given to a tool or a person ("guessed", "pretends").

## Dimension

`stance`: the text reads as a verdict on a person, or as addressed to someone else. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

A tool has a mechanism, and a person given a motive in writing reads it as an accusation. It advises: "lies" also says where a file lies. Measured 2026-10-11 over the adjudicated labeling round: 30.2% precision (13 of 43) before, 81.2% (13 of 16) after. "don't care" is no longer read (every labeled one stated a preference), and quoted words, the writer in the first person, a name ("--pretend", "new-lies.md"), "guessed" before a noun, "lies" closing a "where" clause and an imperative "Pretend" are left alone. It stays off until 30 labeled firings show it.

## Default decision

The code is `PRF2004`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `change-description` | off     |
| `review-reply`       | off     |
| `commit-message`     | off     |
| `issue`              | off     |
| `agent-reply`        | off     |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"intent": "deny"},
  "paths": {"blog/**": {"intent": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "intent", "code": "PRF2004", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/proofread/intent/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/README.md#findings) - the fields every finding carries
