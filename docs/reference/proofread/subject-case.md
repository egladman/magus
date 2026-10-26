---
title: "subject-case"
description: "A house-style rule, off until a decisions table turns it on: it reports a commit subject opening in title case (\"Fix the port\")."
tags: [proofread, rules, subject-case, off]
aliases: [reference/prose/subject-case]
---

# subject-case

A house-style rule, off until a decisions table turns it on: it reports a commit subject opening in title case ("Fix the port").

## What it catches

A commit subject opening in title case ("Fix the port").

## Dimension

`conventions`: a house or genre convention is broken. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

This repository's subjects are lowercase. A capital letter followed by a lowercase letter is title case; an acronym ("URL-parse") and a single capital ("A hook") are left alone, which is the check hack/policy/commits.buzz used to apply off the base branch. A conventional prefix is itself lowercase, so a capital in the description after "feat: " is that branch's grammar, not this rule. Measured 2026-10-25 over the 1765 commits in this repository, 74 open in title case and none of the newest 200 do. House style: off unless a decisions table names it.

## Default decision

The code is `PRF1014`. The decision depends on the kind of text judged:

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
  "rules": {"subject-case": "deny"},
  "paths": {"blog/**": {"subject-case": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "subject-case", "code": "PRF1014", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/subject-case/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
