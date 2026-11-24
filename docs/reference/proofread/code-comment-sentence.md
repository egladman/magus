---
title: "code-comment-sentence"
description: "A deny rule by default: it refuses a code comment sentence over 25 words."
tags: [proofread, rules, code-comment-sentence, deny]
aliases: [reference/prose/code-comment-sentence]
---

# code-comment-sentence

A deny rule by default: it refuses a code comment sentence over 25 words.

## What it catches

A code comment sentence over 25 words.

## Dimension

`economy`: words that carry nothing. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

House style: 29% of magus's exported docs held a sentence over 25 words, against 11.6% in the Go standard library. The excess was sentence length, not the number of sentences.

## Default decision

The code is `PRF6011`. The decision depends on the kind of text judged:

| Kind           | Default |
| -------------- | ------- |
| `code-comment` | deny    |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"code-comment-sentence": "advise"},
  "paths": {"blog/**": {"code-comment-sentence": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "code-comment-sentence", "code": "PRF6011", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/code-comment-sentence/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
