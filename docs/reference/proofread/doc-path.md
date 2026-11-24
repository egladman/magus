---
title: "doc-path"
description: "A deny rule by default: it refuses a code comment citing docs/design, docs/decisions, an ADR number or a Markdown file."
tags: [proofread, rules, doc-path, deny]
aliases: [reference/prose/doc-path]
---

# doc-path

A deny rule by default: it refuses a code comment citing docs/design, docs/decisions, an ADR number or a Markdown file.

## What it catches

A code comment citing docs/design, docs/decisions, an ADR number or a Markdown file.

## Dimension

`conventions`: a house or genre convention is broken. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

House style: a comment that sends the reader to a design page argues the change instead of stating the code. A Go doc link to a symbol stays.

## Default decision

The code is `PRF5013`. The decision depends on the kind of text judged:

| Kind           | Default |
| -------------- | ------- |
| `code-comment` | deny    |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"doc-path": "advise"},
  "paths": {"blog/**": {"doc-path": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "doc-path", "code": "PRF5013", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/doc-path/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
