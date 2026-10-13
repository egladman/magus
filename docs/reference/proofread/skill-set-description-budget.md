---
title: "skill-set-description-budget"
description: "A rule that is off by default: it reports an installed set of skills whose descriptions sum past 2100 words, each skill's short and full form counted once a decisions table turns it on."
tags: [proofread, rules, skill-set-description-budget, off]
aliases: [reference/prose/skill-set-description-budget]
---

# skill-set-description-budget

A rule that is off by default: it reports an installed set of skills whose descriptions sum past 2100 words, each skill's short and full form counted once a decisions table turns it on.

## What it catches

An installed set of skills whose descriptions sum past 2100 words, each skill's short and full form counted.

## Dimension

`economy`: words that carry nothing. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

A per-skill budget passes a set that grows one skill at a time, yet the host loads every description of the set into every session. Measured 2026-10-13 over the 16 skills magus ships with their full twins: 1780 words of short descriptions, back-references included, and 320 of twins at 20 words each, 2100 in all. The budget is that total, a ratchet: a skill added or a description grown advises until another description is cut to make room.

## Default decision

The code is `PRF7022`. The decision depends on the kind of text judged:

| Kind             | Default |
| ---------------- | ------- |
| `skill-template` | off     |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"skill-set-description-budget": "deny"},
  "paths": {"blog/**": {"skill-set-description-budget": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "skill-set-description-budget", "code": "PRF7022", "decision": "off", "url": "https://eli.gladman.cc/magus/reference/proofread/skill-set-description-budget/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/README.md#findings) - the fields every finding carries
