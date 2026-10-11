---
title: "tool-selling"
description: "A deny rule by default: it refuses a word that sells the tool (powerful, seamless, effortless, state-of-the-art) or a buzzword."
tags: [proofread, rules, tool-selling, deny]
aliases: [reference/prose/tool-selling]
---

# tool-selling

A deny rule by default: it refuses a word that sells the tool (powerful, seamless, effortless, state-of-the-art) or a buzzword.

## What it catches

A word that sells the tool (powerful, seamless, effortless, state-of-the-art) or a buzzword.

## Dimension

`stance`: the text reads as a verdict on a person, or as addressed to someone else. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

A selling word names no behavior the model can test a request against, so it adds length without adding a reason to pick the tool. The buzzword list the other kinds use is reused, and the words below are added for descriptions. Over the 22 descriptions magus ships, 0 hold one, so the rule denies on the same footing as buzzword: a hit is a word to replace with what is so.

## Default decision

The code is `PRF9023`. The decision depends on the kind of text judged:

| Kind               | Default |
| ------------------ | ------- |
| `tool-description` | deny    |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"tool-selling": "advise"},
  "paths": {"blog/**": {"tool-selling": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "tool-selling", "code": "PRF9023", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/tool-selling/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
