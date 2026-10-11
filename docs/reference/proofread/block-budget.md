---
title: "block-budget"
description: "A house-style rule, off until a decisions table turns it on: it reports a rule block whose comments, summed, outrun its size."
tags: [proofread, rules, block-budget, off]
aliases: [reference/prose/block-budget]
---

# block-budget

A house-style rule, off until a decisions table turns it on: it reports a rule block whose comments, summed, outrun its size.

## What it catches

A rule block whose comments, summed, outrun its size.

## Dimension

`economy`: words that carry nothing. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

House style: a block earns 6 comment lines and one more for every 5 statements in it, the way a longer function earns a longer comment. Comments past that are docs kept in the wrong place.

## Default decision

The code is `PRF6009`. The decision depends on the kind of text judged:

| Kind         | Default |
| ------------ | ------- |
| `stylesheet` | off     |

House style ships `off`: the rule encodes one repository's conventions, not
a rule of writing a teammate reads. A repository turns it on in its decisions table.

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"block-budget": "deny"},
  "paths": {"blog/**": {"block-budget": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "block-budget", "code": "PRF6009", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/block-budget/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
