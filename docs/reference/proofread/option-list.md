---
title: "option-list"
description: "An advisory by default: it explains, and blocks nothing, on an agent reply that lays out labeled options or alternatives."
tags: [proofread, rules, option-list, advise]
aliases: [reference/prose/option-list]
---

# option-list

An advisory by default: it explains, and blocks nothing, on an agent reply that lays out labeled options or alternatives.

## What it catches

An agent reply that lays out labeled options or alternatives.

## Dimension

`structure`: the reader has to reconstruct the order or the purpose. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

Options the person did not ask for move a decision the agent could make back to them. Measured 2026-10-10: it fires on 6 of the 400 replies (1.5%), and a broader pattern found 158 of all 7088 (2.2%). It advises: the rule cannot see whether the person asked for the choices.

## Default decision

The code is `PRF8023`. The decision depends on the kind of text judged:

| Kind          | Default |
| ------------- | ------- |
| `agent-reply` | advise  |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"option-list": "deny"},
  "paths": {"blog/**": {"option-list": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "option-list", "code": "PRF8023", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/proofread/option-list/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
