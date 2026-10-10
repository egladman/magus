---
title: "intent"
description: "An advisory by default: it explains, and blocks nothing, on a motive given to a tool or a person (\"guessed\", \"pretends\")."
tags: [prose, rules, intent, advise]
---

# intent

An advisory by default: it explains, and blocks nothing, on a motive given to a tool or a person ("guessed", "pretends").

## What it catches

A motive given to a tool or a person ("guessed", "pretends").

## Why

A tool has a mechanism, and a person given a motive in writing reads it as an accusation. It advises: "lies" also says where a file lies.

## Default decision

The code is `PRS2004`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `change-description` | advise  |
| `review-reply`       | advise  |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. The prose judge reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"intent": "deny"},
  "paths": {"blog/**": {"intent": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "intent", "code": "PRS2004", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/prose/intent/"}
```

The judge's `-catalog` flag prints this entry with every other rule's.

## See also

- [All prose rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
