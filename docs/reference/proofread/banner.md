---
title: "banner"
description: "A deny rule by default: it refuses a banner line of dashes, equals signs or slashes in a code comment."
tags: [proofread, rules, banner, deny]
aliases: [reference/prose/banner]
---

# banner

A deny rule by default: it refuses a banner line of dashes, equals signs or slashes in a code comment.

## What it catches

A banner line of dashes, equals signs or slashes in a code comment.

## Dimension

`structure`: the reader has to reconstruct the order or the purpose. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

House style: a banner frames a section, and a file that wants sections wants splitting. It costs a line and says nothing.

## Default decision

The code is `PRF6012`. The decision depends on the kind of text judged:

| Kind           | Default |
| -------------- | ------- |
| `code-comment` | deny    |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"banner": "advise"},
  "paths": {"blog/**": {"banner": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "banner", "code": "PRF6012", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/banner/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
