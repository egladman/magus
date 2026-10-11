---
title: "filler"
description: "A rule that denies on `doc-comment`, `reference`, `guide`, `change-description`, `agent-instructions`, `commit-message`, `issue`, `release-notes`, `changelog`, `cli-help`, and `review-reply` and advises on `agent-reply` by default: it reports throat-clearing (\"Note that\") and filler adverbs (\"simply\", \"basically\")."
tags: [proofread, rules, filler, deny]
aliases: [reference/prose/filler]
---

# filler

A rule that denies on `doc-comment`, `reference`, `guide`, `change-description`, `agent-instructions`, `commit-message`, `issue`, `release-notes`, `changelog`, `cli-help`, and `review-reply` and advises on `agent-reply` by default: it reports throat-clearing ("Note that") and filler adverbs ("simply", "basically").

## What it catches

Throat-clearing ("Note that") and filler adverbs ("simply", "basically").

## Dimension

`economy`: words that carry nothing. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

The words carry nothing the sentence needs. Written text takes a wider list ("actually", "robust") than doc comments, which keep the narrow one until a sweep clears the wider; the senses that carry meaning ("just" as merely, "very" as the same one) are exempt.

## Default decision

The code is `PRF4001`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `doc-comment`        | deny    |
| `reference`          | deny    |
| `guide`              | deny    |
| `change-description` | deny    |
| `agent-instructions` | deny    |
| `commit-message`     | deny    |
| `issue`              | deny    |
| `release-notes`      | deny    |
| `changelog`          | deny    |
| `cli-help`           | deny    |
| `review-reply`       | deny    |
| `agent-reply`        | advise  |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"filler": "advise"},
  "paths": {"blog/**": {"filler": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "filler", "code": "PRF4001", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/filler/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
