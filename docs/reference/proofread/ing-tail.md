---
title: "ing-tail"
description: "A rule that is off by default: it reports a participle clause added to claim significance (\", highlighting the importance of\") once a decisions table turns it on."
tags: [proofread, rules, ing-tail, off]
aliases: [reference/prose/ing-tail]
---

# ing-tail

A rule that is off by default: it reports a participle clause added to claim significance (", highlighting the importance of") once a decisions table turns it on.

## What it catches

A participle clause added to claim significance (", highlighting the importance of").

## Dimension

`economy`: words that carry nothing. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

The tail asserts significance with no subject to own it. It advises: a participle clause is also ordinary grammar. Measured 2026-10-11 over the adjudicated labeling round 2: 41.9% precision (18 of 43) before, 85.7% (18 of 21) after. "reflecting" is no longer read (it named what a change mirrors), and a tail with no object (a noun in a list), a presenting verb before the bare content it names ("showcasing custom validators"), an item in a list of actions and quoted words are left alone. It stays off until a fresh labeling round measures it.

## Default decision

The code is `PRF4011`. The decision depends on the kind of text judged:

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
| `agent-reply`        | off     |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"ing-tail": "deny"},
  "paths": {"blog/**": {"ing-tail": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "ing-tail", "code": "PRF4011", "decision": "off", "url": "https://eli.gladman.cc/magus/reference/proofread/ing-tail/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
