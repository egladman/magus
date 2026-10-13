---
title: "nit-label"
description: "A rule that is off by default: it reports an inline review comment labeled nit on a blocking demand, or blocking on an optional one once a decisions table turns it on."
tags: [proofread, rules, nit-label, off]
aliases: [reference/prose/nit-label]
---

# nit-label

A rule that is off by default: it reports an inline review comment labeled nit on a blocking demand, or blocking on an optional one once a decisions table turns it on.

## What it catches

An inline review comment labeled nit on a blocking demand, or blocking on an optional one.

## Dimension

`stance`: the text reads as a verdict on a person, or as addressed to someone else. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

Conventional Comments (conventionalcomments.org) holds a nitpick non-blocking by definition, and Google's review guide (eng-practices, "How to write code review comments") labels optional points "Nit:" so the author knows what must change. A label the text contradicts leaves the author to guess which one the reviewer meant. It is a convention, not a study, so it advises.

## Default decision

The code is `PRF8031`. The decision depends on the kind of text judged:

| Kind            | Default |
| --------------- | ------- |
| `review-inline` | off     |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"nit-label": "deny"},
  "paths": {"blog/**": {"nit-label": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "nit-label", "code": "PRF8031", "decision": "off", "url": "https://eli.gladman.cc/magus/reference/proofread/nit-label/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/README.md#findings) - the fields every finding carries
