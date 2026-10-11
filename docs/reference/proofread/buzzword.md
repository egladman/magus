---
title: "buzzword"
description: "A deny rule by default: it refuses a word chosen to sound significant rather than to say what is so (\"delve\", \"tapestry\")."
tags: [proofread, rules, buzzword, deny]
aliases: [reference/prose/buzzword]
---

# buzzword

A deny rule by default: it refuses a word chosen to sound significant rather than to say what is so ("delve", "tapestry").

## What it catches

A word chosen to sound significant rather than to say what is so ("delve", "tapestry").

## Dimension

`economy`: words that carry nothing. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

The list holds words with no plain sense in technical text. Zero hits over the docs pages, changelog fragments and merged pull requests. Measured 2026-10-10 before a fix: 85.7% precision on its cases (6 of 7), firing on 0.1% of AIDev pull requests and 1 magus commit body; of 40 pull request firings read, 18 were wrong: "on the same page" as a page (7), "deep dive" naming a document (5), "vibrant" as a color (4), "underscores" as the character. Those now count only in their buzzword sense: after a person, before into or ending the phrase, before an abstract noun, and as a verb. After: 100% on its cases; firing rates measured at merge.

## Default decision

The code is `PRF4006`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `reference`          | deny    |
| `guide`              | deny    |
| `change-description` | deny    |
| `agent-instructions` | deny    |
| `commit-message`     | deny    |
| `issue`              | deny    |
| `release-notes`      | deny    |
| `changelog`          | deny    |
| `review-reply`       | deny    |
| `agent-reply`        | deny    |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"buzzword": "advise"},
  "paths": {"blog/**": {"buzzword": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "buzzword", "code": "PRF4006", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/buzzword/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
