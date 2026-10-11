---
title: "help-length"
description: "A deny rule by default: it refuses help text over 240 runes."
tags: [proofread, rules, help-length, deny]
aliases: [reference/prose/help-length]
---

# help-length

A deny rule by default: it refuses help text over 240 runes.

## What it catches

Help text over 240 runes.

## Dimension

`economy`: words that carry nothing. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

A flag's help wraps in a table of flags, so a long one pushes the next flag off the screen. Over the same 326 strings the median is 69 runes and the 90th percentile 146; 5 run past 240, and the longest is 364.

## Default decision

The code is `PRF9011`. The decision depends on the kind of text judged:

| Kind       | Default |
| ---------- | ------- |
| `cli-help` | deny    |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"help-length": "advise"},
  "paths": {"blog/**": {"help-length": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "help-length", "code": "PRF9011", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/help-length/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
