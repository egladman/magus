---
title: "request-recap"
description: "An advisory by default: it explains, and blocks nothing, on an agent reply that opens by restating what the person asked."
tags: [proofread, rules, request-recap, advise]
aliases: [reference/prose/request-recap]
---

# request-recap

An advisory by default: it explains, and blocks nothing, on an agent reply that opens by restating what the person asked.

## What it catches

An agent reply that opens by restating what the person asked.

## Dimension

`economy`: words that carry nothing. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

The person knows what they asked, and a restatement delays the answer. Measured 2026-10-10: it fires on 1 of the 400 replies, and 3 of all 7088 end-of-turn replies on that machine open this way, too few to measure precision. It advises: a restatement can also correct a misreading.

## Default decision

The code is `PRF8022`. The decision depends on the kind of text judged:

| Kind          | Default |
| ------------- | ------- |
| `agent-reply` | advise  |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"request-recap": "deny"},
  "paths": {"blog/**": {"request-recap": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "request-recap", "code": "PRF8022", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/proofread/request-recap/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
