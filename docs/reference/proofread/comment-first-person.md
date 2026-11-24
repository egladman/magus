---
title: "comment-first-person"
description: "A deny rule by default: it refuses I, me, my, we, us or our in a code comment."
tags: [proofread, rules, comment-first-person, deny]
aliases: [reference/prose/comment-first-person]
---

# comment-first-person

A deny rule by default: it refuses I, me, my, we, us or our in a code comment.

## What it catches

I, me, my, we, us or our in a code comment.

## Dimension

`stance`: the text reads as a verdict on a person, or as addressed to someone else. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

House style: a comment states what the code does and why, not who chose it or how it felt.

## Default decision

The code is `PRF5015`. The decision depends on the kind of text judged:

| Kind           | Default |
| -------------- | ------- |
| `code-comment` | deny    |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"comment-first-person": "advise"},
  "paths": {"blog/**": {"comment-first-person": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "comment-first-person", "code": "PRF5015", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/comment-first-person/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
