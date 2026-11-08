---
title: "local-branch"
description: "A house-style rule, off until a decisions table turns it on: it reports a branch that exists only on the author's machine, from the names the caller passes."
tags: [proofread, rules, local-branch, off]
aliases: [reference/prose/local-branch]
---

# local-branch

A house-style rule, off until a decisions table turns it on: it reports a branch that exists only on the author's machine, from the names the caller passes.

## What it catches

A branch that exists only on the author's machine, from the names the caller passes.

## Dimension

`conventions`: a house or genre convention is broken. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

House style: a branch that was never pushed resolves on no reader's machine, the way a home directory path does not. Proofread cannot tell a local branch from text, so the caller lists the local-only names; with none, the rule judges nothing. A one-word name counts only in backticks.

## Default decision

The code is `PRF5009`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `change-description` | off     |
| `review-summary`     | off     |
| `review-inline`      | off     |
| `review-reply`       | off     |
| `commit-message`     | off     |
| `issue`              | off     |
| `release-notes`      | off     |
| `changelog`          | off     |

House style ships `off`: the rule encodes one repository's conventions, not
a rule of plain writing. A repository turns it on in its decisions table.

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"local-branch": "deny"},
  "paths": {"blog/**": {"local-branch": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "local-branch", "code": "PRF5009", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/local-branch/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
