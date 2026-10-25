---
title: "no-body"
description: "A house-style rule, off until a decisions table turns it on: it reports a commit message with a prose body after the subject."
tags: [proofread, rules, no-body, off]
aliases: [reference/prose/no-body]
---

# no-body

A house-style rule, off until a decisions table turns it on: it reports a commit message with a prose body after the subject.

## What it catches

A commit message with a prose body after the subject.

## Dimension

`structure`: the reader has to reconstruct the order or the purpose. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

Detail belongs in the pull request description, so a commit is one subject line. A trailer block ("Key: value" lines, including "BREAKING CHANGE") is not a body, and neither is a blank line of padding; body-separator leaves the same trailer block alone when a repository allows a body. A line a squash merge writes, opening "* ", is the forge's list of the commits it folded, not prose. Measured 2026-10-25 over the 1765 commits in this repository, 354 messages had a body and 318 of those opened with that list. House style: off unless a decisions table names it.

## Default decision

The code is `PRF1016`. The decision depends on the kind of text judged:

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
  "rules": {"no-body": "deny"},
  "paths": {"blog/**": {"no-body": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "no-body", "code": "PRF1016", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/no-body/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
