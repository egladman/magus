---
title: "comment-history"
description: "A deny rule by default: it refuses a code comment that narrates a change rather than the code."
tags: [proofread, rules, comment-history, deny]
aliases: [reference/prose/comment-history]
---

# comment-history

A deny rule by default: it refuses a code comment that narrates a change rather than the code.

## What it catches

A code comment that narrates a change rather than the code.

## Dimension

`economy`: words that carry nothing. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

House style: a comment that says what the code replaced, or what it was before, only makes sense against the diff. The history rule's phrases and the stylesheet ones both count here.

## Default decision

The code is `PRF6013`. The decision depends on the kind of text judged:

| Kind           | Default |
| -------------- | ------- |
| `code-comment` | deny    |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"comment-history": "advise"},
  "paths": {"blog/**": {"comment-history": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "comment-history", "code": "PRF6013", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/comment-history/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
