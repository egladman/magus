---
title: "help-sentence"
description: "A deny rule by default: it refuses a sentence of help text over 40 words."
tags: [proofread, rules, help-sentence, deny]
aliases: [reference/prose/help-sentence]
---

# help-sentence

A deny rule by default: it refuses a sentence of help text over 40 words.

## What it catches

A sentence of help text over 40 words.

## Dimension

`economy`: words that carry nothing. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

A reader scans help in a terminal while deciding what to type. The federal plain-language quick tips ask for no sentence over 40 words. Over the 326 flag usage strings magus binds (median 11 words, 90th percentile 25, longest 56) one runs past it.

## Default decision

The code is `PRF9010`. The decision depends on the kind of text judged:

| Kind       | Default |
| ---------- | ------- |
| `cli-help` | deny    |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"help-sentence": "advise"},
  "paths": {"blog/**": {"help-sentence": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "help-sentence", "code": "PRF9010", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/help-sentence/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
