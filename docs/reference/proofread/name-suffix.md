---
title: "name-suffix"
description: "A house-style rule, off until a decisions table turns it on: it reports a function or method name whose last word is Of or For."
tags: [proofread, rules, name-suffix, off]
aliases: [reference/prose/name-suffix]
---

# name-suffix

A house-style rule, off until a decisions table turns it on: it reports a function or method name whose last word is Of or For.

## What it catches

A function or method name whose last word is Of or For.

## Dimension

`conventions`: a house or genre convention is broken. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

House style: this repository names a function for what it returns or does, so `valueOf` and `configFor` are renamed.

## Default decision

The code is `PRF6003`. The decision depends on the kind of text judged:

| Kind          | Default |
| ------------- | ------- |
| `doc-comment` | off     |

House style ships `off`: the rule encodes one repository's conventions, not
a rule of writing a teammate reads. A repository turns it on in its decisions table.

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"name-suffix": "deny"},
  "paths": {"blog/**": {"name-suffix": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "name-suffix", "code": "PRF6003", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/name-suffix/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
