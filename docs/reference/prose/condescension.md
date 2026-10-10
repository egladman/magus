---
title: "condescension"
description: "A deny rule by default: it refuses a word that tells the reader how hard a step should feel or what they should already know."
tags: [prose, rules, condescension, deny]
---

# condescension

A deny rule by default: it refuses a word that tells the reader how hard a step should feel or what they should already know.

## What it catches

A word that tells the reader how hard a step should feel or what they should already know.

## Why

"Simply run" and "of course" tell a reader who is stuck that they should not be. Measured over the guides before the rule, none of 16 lowercase "just" minimized a step, so "just" counts only before a verb the reader carries out, and an "easy" that warns ("easy to get wrong") is left alone.

## Default decision

The code is `PRS2006`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `reference`          | deny    |
| `guide`              | deny    |
| `change-description` | deny    |
| `agent-instructions` | deny    |
| `review-reply`       | deny    |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. The prose judge reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"condescension": "advise"},
  "paths": {"blog/**": {"condescension": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "condescension", "code": "PRS2006", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/prose/condescension/"}
```

The judge's `-catalog` flag prints this entry with every other rule's.

## See also

- [All prose rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
