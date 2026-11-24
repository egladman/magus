---
title: "code-comment-budget"
description: "A deny rule by default: it refuses a code comment block over the words or prose lines its place earns."
tags: [proofread, rules, code-comment-budget, deny]
aliases: [reference/prose/code-comment-budget]
---

# code-comment-budget

A deny rule by default: it refuses a code comment block over the words or prose lines its place earns.

## What it catches

A code comment block over the words or prose lines its place earns.

## Dimension

`economy`: words that carry nothing. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

House style: magus's exported docs ran to 83 words at p90 against 74 in the Go standard library. A trailing comment earns 12 words on 1 line, one inside a body 30 on 3, a doc on an unexported declaration 40 on 4, and an exported doc or a file header 60 on 8. Blank lines, code, tables, URLs and directives count for nothing. A project may only tighten them.

## Default decision

The code is `PRF6010`. The decision depends on the kind of text judged:

| Kind           | Default |
| -------------- | ------- |
| `code-comment` | deny    |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"code-comment-budget": "advise"},
  "paths": {"blog/**": {"code-comment-budget": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "code-comment-budget", "code": "PRF6010", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/code-comment-budget/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
