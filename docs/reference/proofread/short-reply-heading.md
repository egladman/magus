---
title: "short-reply-heading"
description: "An advisory by default: it explains, and blocks nothing, on a heading in an agent reply under 300 words."
tags: [proofread, rules, short-reply-heading, advise]
aliases: [reference/prose/short-reply-heading]
---

# short-reply-heading

An advisory by default: it explains, and blocks nothing, on a heading in an agent reply under 300 words.

## What it catches

A heading in an agent reply under 300 words.

## Dimension

`structure`: the reader has to reconstruct the order or the purpose. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

A reply that short reads in one screen, and a heading there splits it into a page the person has to navigate. Measured 2026-10-10 over the 400 replies: 64 carry a heading, and it fires on 17 (4.3%) whose prose runs under 300 words. It advises: a heading can still mark the one decision the person owes.

## Default decision

The code is `PRF8025`. The decision depends on the kind of text judged:

| Kind          | Default |
| ------------- | ------- |
| `agent-reply` | advise  |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"short-reply-heading": "deny"},
  "paths": {"blog/**": {"short-reply-heading": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "short-reply-heading", "code": "PRF8025", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/proofread/short-reply-heading/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
