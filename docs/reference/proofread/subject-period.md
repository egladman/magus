---
title: "subject-period"
description: "A deny rule by default: it refuses a commit subject ending in a period."
tags: [proofread, rules, subject-period, deny]
aliases: [reference/prose/subject-period]
---

# subject-period

A deny rule by default: it refuses a commit subject ending in a period.

## What it catches

A commit subject ending in a period.

## Why

A subject is a title, and a title carries no full stop. A subject ending in "..." is left alone. None of the 1729 commit messages on main ends in one.

## Default decision

The code is `PRF1012`. The decision depends on the kind of text judged:

| Kind             | Default |
| ---------------- | ------- |
| `commit-message` | deny    |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"subject-period": "advise"},
  "paths": {"blog/**": {"subject-period": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "subject-period", "code": "PRF1012", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/subject-period/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
