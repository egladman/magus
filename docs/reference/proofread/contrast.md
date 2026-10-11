---
title: "contrast"
description: "A rule that denies on `change-description` and advises on `reference`, `guide`, `agent-instructions`, `commit-message`, `issue`, `release-notes`, and `changelog` by default: it reports a claim made by denying its opposite first (\"not just X, it is Y\")."
tags: [proofread, rules, contrast, deny]
aliases: [reference/prose/contrast]
---

# contrast

A rule that denies on `change-description` and advises on `reference`, `guide`, `agent-instructions`, `commit-message`, `issue`, `release-notes`, and `changelog` by default: it reports a claim made by denying its opposite first ("not just X, it is Y").

## What it catches

A claim made by denying its opposite first ("not just X, it is Y").

## Dimension

`economy`: words that carry nothing. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

Negative parallelism argues with a position nobody took. It denies in a change description, where 200 merged pull requests used it 0 times, and advises elsewhere, where the docs use it deliberately 15 times.

## Default decision

The code is `PRF4010`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `reference`          | advise  |
| `guide`              | advise  |
| `change-description` | deny    |
| `agent-instructions` | advise  |
| `commit-message`     | advise  |
| `issue`              | advise  |
| `release-notes`      | advise  |
| `changelog`          | advise  |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"contrast": "deny"},
  "paths": {"blog/**": {"contrast": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "contrast", "code": "PRF4010", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/contrast/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
