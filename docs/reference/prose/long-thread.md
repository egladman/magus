---
title: "long-thread"
description: "An advisory by default: it explains, and blocks nothing, on a review reply that is its author's fourth or later in a thread."
tags: [prose, rules, long-thread, advise]
---

# long-thread

An advisory by default: it explains, and blocks nothing, on a review reply that is its author's fourth or later in a thread.

## What it catches

A review reply that is its author's fourth or later in a thread.

## Why

A long exchange in text reads as a stalemate to the people watching it, and a call settles it faster. It needs the thread length (-thread-length), and stays silent without it.

## Default decision

The code is `PRS8004`. The decision depends on the kind of text judged:

| Kind           | Default |
| -------------- | ------- |
| `review-reply` | advise  |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. The prose judge reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"long-thread": "deny"},
  "paths": {"blog/**": {"long-thread": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "long-thread", "code": "PRS8004", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/prose/long-thread/"}
```

The judge's `-catalog` flag prints this entry with every other rule's.

## See also

- [All prose rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
