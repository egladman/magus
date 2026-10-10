---
title: "staccato"
description: "A rule that denies on `change-description` and advises on `reference` by default: it reports three or more consecutive sentences of six words or fewer in one paragraph."
tags: [prose, rules, staccato, deny]
---

# staccato

A rule that denies on `change-description` and advises on `reference` by default: it reports three or more consecutive sentences of six words or fewer in one paragraph.

## What it catches

Three or more consecutive sentences of six words or fewer in one paragraph.

## Why

A run of fragments reads as a drumbeat. At a cap of 4 words the rule found nothing and missed a real run; at 6 it found that run and docs/scope.md alone. It denies in a change description and advises on a page; a guide's steps and agent instructions are short by rule, so it does not judge them.

## Default decision

The code is `PRS4012`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `reference`          | advise  |
| `change-description` | deny    |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. The prose judge reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"staccato": "deny"},
  "paths": {"blog/**": {"staccato": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "staccato", "code": "PRS4012", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/prose/staccato/"}
```

The judge's `-catalog` flag prints this entry with every other rule's.

## See also

- [All prose rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
