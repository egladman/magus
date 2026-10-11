---
title: "staccato"
description: "A rule that denies on `change-description`, `issue`, and `release-notes` and advises on `reference` by default: it reports three or more consecutive sentences of six words or fewer in one paragraph."
tags: [proofread, rules, staccato, deny]
aliases: [reference/prose/staccato]
---

# staccato

A rule that denies on `change-description`, `issue`, and `release-notes` and advises on `reference` by default: it reports three or more consecutive sentences of six words or fewer in one paragraph.

## What it catches

Three or more consecutive sentences of six words or fewer in one paragraph.

## Dimension

`structure`: the reader has to reconstruct the order or the purpose. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

A run of fragments reads as a drumbeat. At a cap of 4 words the rule found nothing and missed a real run; at 6 it found that run and docs/scope.md alone. It denies in a change description and advises on a page; a guide's steps and agent instructions are short by rule, so it does not judge them.

## Default decision

The code is `PRF4012`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `reference`          | advise  |
| `change-description` | deny    |
| `issue`              | deny    |
| `release-notes`      | deny    |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
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
{"rule": "staccato", "code": "PRF4012", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/staccato/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
