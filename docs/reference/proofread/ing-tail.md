---
title: "ing-tail"
description: "An advisory by default: it explains, and blocks nothing, on a participle clause added to claim significance (\", highlighting the importance of\")."
tags: [proofread, rules, ing-tail, advise]
aliases: [reference/prose/ing-tail]
---

# ing-tail

An advisory by default: it explains, and blocks nothing, on a participle clause added to claim significance (", highlighting the importance of").

## What it catches

A participle clause added to claim significance (", highlighting the importance of").

## Why

The tail asserts significance with no subject to own it. It advises: a participle clause is also ordinary grammar.

## Default decision

The code is `PRF4011`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `reference`          | advise  |
| `guide`              | advise  |
| `change-description` | advise  |
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
  "rules": {"ing-tail": "deny"},
  "paths": {"blog/**": {"ing-tail": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "ing-tail", "code": "PRF4011", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/proofread/ing-tail/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
