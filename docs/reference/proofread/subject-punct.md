---
title: "subject-punct"
description: "A house-style rule, off until a decisions table turns it on: it reports a comma, parenthesis, exclamation mark, question mark or spaced hyphen in a commit subject."
tags: [proofread, rules, subject-punct, off]
aliases: [reference/prose/subject-punct]
---

# subject-punct

A house-style rule, off until a decisions table turns it on: it reports a comma, parenthesis, exclamation mark, question mark or spaced hyphen in a commit subject.

## What it catches

A comma, parenthesis, exclamation mark, question mark or spaced hyphen in a commit subject.

## Dimension

`conventions`: a house or genre convention is broken. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

Clauses in a subject join with a semicolon. A comma, a parenthesis, an exclamation mark, a question mark or a spaced hyphen is punctuation this repository's subjects do not use. A conventional prefix, "type(scope)!: ", is not part of what the subject says, so a parenthesis or bang there is left alone, and a " (#123)" a forge appends is not counted, the same as subject-length. A colon is not this rule's: the caller knows whether the branch requires that prefix. Measured 2026-10-25 over the 1765 commits in this repository, 705 subjects had a parenthesis and 502 of those end in that trailer. House style: off unless a decisions table names it.

## Default decision

The code is `PRF1015`. The decision depends on the kind of text judged:

| Kind             | Default |
| ---------------- | ------- |
| `commit-message` | off     |

House style ships `off`: the rule encodes one repository's conventions, not
a rule of plain writing. A repository turns it on in its decisions table.

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"subject-punct": "deny"},
  "paths": {"blog/**": {"subject-punct": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "subject-punct", "code": "PRF1015", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/subject-punct/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
