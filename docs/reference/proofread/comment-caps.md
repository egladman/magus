---
title: "comment-caps"
description: "A deny rule by default: it refuses an ordinary word in capitals for emphasis in a code comment."
tags: [proofread, rules, comment-caps, deny]
aliases: [reference/prose/comment-caps]
---

# comment-caps

A deny rule by default: it refuses an ordinary word in capitals for emphasis in a code comment.

## What it catches

An ordinary word in capitals for emphasis in a code comment.

## Dimension

`conventions`: a house or genre convention is broken. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

House style: SHOUTING a word adds no fact. An acronym and a name the file's code spells pass.

## Default decision

The code is `PRF5014`. The decision depends on the kind of text judged:

| Kind           | Default |
| -------------- | ------- |
| `code-comment` | deny    |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"comment-caps": "advise"},
  "paths": {"blog/**": {"comment-caps": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "comment-caps", "code": "PRF5014", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/comment-caps/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
