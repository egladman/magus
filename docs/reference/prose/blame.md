---
title: "blame"
description: "A deny rule by default: it refuses a person or past work as the subject of a fault, and contempt for code or a decision."
tags: [prose, rules, blame, deny]
---

# blame

A deny rule by default: it refuses a person or past work as the subject of a fault, and contempt for code or a decision.

## What it catches

A person or past work as the subject of a fault, and contempt for code or a decision.

## Why

Text loses its tone on the way to a reader, who fills the gap with intent the writer never had. "Whoever wrote this forgot to" reads as an accusation; "the rename left the old key" states the same fact. It measured no false positive over the last 200 merged pull requests, so it denies. The first person is left alone, since owning a fault reads as candor.

## Default decision

The code is `PRS2001`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `change-description` | deny    |
| `review-reply`       | deny    |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. The prose judge reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"blame": "advise"},
  "paths": {"blog/**": {"blame": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "blame", "code": "PRS2001", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/prose/blame/"}
```

The judge's `-catalog` flag prints this entry with every other rule's.

## See also

- [All prose rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
