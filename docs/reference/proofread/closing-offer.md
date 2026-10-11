---
title: "closing-offer"
description: "An advisory by default: it explains, and blocks nothing, on an agent reply whose last paragraph offers more work or asks leave to go on."
tags: [proofread, rules, closing-offer, advise]
aliases: [reference/prose/closing-offer]
---

# closing-offer

An advisory by default: it explains, and blocks nothing, on an agent reply whose last paragraph offers more work or asks leave to go on.

## What it catches

An agent reply whose last paragraph offers more work or asks leave to go on.

## Dimension

`stance`: the text reads as a verdict on a person, or as addressed to someone else. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

A reflexive offer hands the next decision back to the person and costs them a turn. Measured 2026-10-10 over the same 400 replies: it fires on 62 (15.5%), "Want me to" in most. Of 45 closing offers read, about a third asked for a decision the agent needed (a push, a branch name, a choice between designs), so it advises: the rule cannot tell a needed question from a reflexive one.

## Default decision

The code is `PRF8021`. The decision depends on the kind of text judged:

| Kind          | Default |
| ------------- | ------- |
| `agent-reply` | advise  |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"closing-offer": "deny"},
  "paths": {"blog/**": {"closing-offer": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "closing-offer", "code": "PRF8021", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/proofread/closing-offer/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
