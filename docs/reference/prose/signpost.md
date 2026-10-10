---
title: "signpost"
description: "A deny rule by default: it refuses an announcement standing where the point should be (\"Here's the thing\", \"Let's dive in\")."
tags: [prose, rules, signpost, deny]
---

# signpost

A deny rule by default: it refuses an announcement standing where the point should be ("Here's the thing", "Let's dive in").

## What it catches

An announcement standing where the point should be ("Here's the thing", "Let's dive in").

## Why

A signpost delays the point it promises. It had zero hits in 261 docs pages, 665 changelog fragments and 200 merged pull requests, so it denies at no cost.

## Default decision

The code is `PRS4003`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `reference`          | deny    |
| `guide`              | deny    |
| `change-description` | deny    |
| `agent-instructions` | deny    |
| `review-reply`       | deny    |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. The prose judge reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"signpost": "advise"},
  "paths": {"blog/**": {"signpost": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "signpost", "code": "PRS4003", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/prose/signpost/"}
```

The judge's `-catalog` flag prints this entry with every other rule's.

## See also

- [All prose rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
