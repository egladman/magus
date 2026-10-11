---
title: "credit"
description: "An advisory by default: it explains, and blocks nothing, on a change description that removes or replaces something and says nothing of what it was for."
tags: [proofread, rules, credit, advise]
aliases: [reference/prose/credit]
---

# credit

An advisory by default: it explains, and blocks nothing, on a change description that removes or replaces something and says nothing of what it was for.

## What it catches

A change description that removes or replaces something and says nothing of what it was for.

## Dimension

`stance`: the text reads as a verdict on a person, or as addressed to someone else. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

One clause on what the earlier design did well keeps the change from reading as a verdict on its author. Over the last 200 merged pull requests, 16 removed something by a clause of the title or a sentence's opening, and none said what it had been for. It advises until it has fired on real text and every firing was right.

## Default decision

The code is `PRF2005`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `change-description` | advise  |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"credit": "deny"},
  "paths": {"blog/**": {"credit": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "credit", "code": "PRF2005", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/proofread/credit/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
