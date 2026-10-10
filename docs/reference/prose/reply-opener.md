---
title: "reply-opener"
description: "A deny rule by default: it refuses a sentence of a review reply that opens by contradicting (\"No,\", \"As I said\")."
tags: [prose, rules, reply-opener, deny]
---

# reply-opener

A deny rule by default: it refuses a sentence of a review reply that opens by contradicting ("No,", "As I said").

## What it catches

A sentence of a review reply that opens by contradicting ("No,", "As I said").

## Why

A contradiction first reads as winning an argument whatever follows it. It measured no false positive, so it denies.

## Default decision

The code is `PRS8001`. The decision depends on the kind of text judged:

| Kind           | Default |
| -------------- | ------- |
| `review-reply` | deny    |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. The prose judge reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"reply-opener": "advise"},
  "paths": {"blog/**": {"reply-opener": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "reply-opener", "code": "PRS8001", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/prose/reply-opener/"}
```

The judge's `-catalog` flag prints this entry with every other rule's.

## See also

- [All prose rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
