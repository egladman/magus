---
title: "subject-length"
description: "A deny rule by default: it refuses a commit subject over 100 bytes."
tags: [proofread, rules, subject-length, deny]
aliases: [reference/prose/subject-length]
---

# subject-length

A deny rule by default: it refuses a commit subject over 100 bytes.

## What it catches

A commit subject over 100 bytes.

## Dimension

`structure`: the reader has to reconstruct the order or the purpose. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

A one-line log cuts a long subject off. The cap is 100 bytes, commitlint's header-max-length and the limit this repository's commit hook applies, not git's customary 72, since a semicolon joining two clauses already runs past 72 on main. A " (#123)" a forge appends is not counted. Over the 1729 commit messages on main, 485 run past 100, but 2 of the newest 120 do: the limit arrived with the commit hook. The number is fixed in the rule; a decisions table sets the rule off, advise or deny.

## Default decision

The code is `PRF1011`. The decision depends on the kind of text judged:

| Kind             | Default |
| ---------------- | ------- |
| `commit-message` | deny    |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"subject-length": "advise"},
  "paths": {"blog/**": {"subject-length": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "subject-length", "code": "PRF1011", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/subject-length/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
