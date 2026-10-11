---
title: "agreement-opener"
description: "An advisory by default: it explains, and blocks nothing, on an agent reply that opens with praise or agreement (\"Great question\", \"You're right\")."
tags: [proofread, rules, agreement-opener, advise]
aliases: [reference/prose/agreement-opener]
---

# agreement-opener

An advisory by default: it explains, and blocks nothing, on an agent reply that opens with praise or agreement ("Great question", "You're right").

## What it catches

An agent reply that opens with praise or agreement ("Great question", "You're right").

## Dimension

`stance`: the text reads as a verdict on a person, or as addressed to someone else. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

Praise or agreement first answers the person's tone rather than their request, and pushes the result down. Measured 2026-10-10 over the 400 replies: it fires on 15 (3.8%), 10 of them "You're right" or "You were right". It advises: "You're right" can concede a correction the person needs to hear was taken.

## Default decision

The code is `PRF8026`. The decision depends on the kind of text judged:

| Kind          | Default |
| ------------- | ------- |
| `agent-reply` | advise  |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"agreement-opener": "deny"},
  "paths": {"blog/**": {"agreement-opener": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "agreement-opener", "code": "PRF8026", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/proofread/agreement-opener/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
