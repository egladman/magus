---
title: "comment-block"
description: "A house-style rule, off until a decisions table turns it on: it reports a doc comment over 250 words."
tags: [proofread, rules, comment-block, off]
aliases: [reference/prose/comment-block]
---

# comment-block

A house-style rule, off until a decisions table turns it on: it reports a doc comment over 250 words.

## What it catches

A doc comment over 250 words.

## Dimension

`economy`: words that carry nothing. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

House style: measured 2026-10-06 over 46664 comment blocks, a block's p50 is 25 words, p90 72 and p99 168; the cap sits past p99 and catches a design document living in a comment.

## Default decision

The code is `PRF6001`. The decision depends on the kind of text judged:

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
  "rules": {"comment-block": "deny"},
  "paths": {"blog/**": {"comment-block": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "comment-block", "code": "PRF6001", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/comment-block/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
