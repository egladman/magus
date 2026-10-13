---
title: "impatience"
description: "An advisory by default: it explains, and blocks nothing, on a review reply that reads as impatient: \"as I said\", \"for the last time\", \"obviously\"."
tags: [proofread, rules, impatience, advise]
aliases: [reference/prose/impatience]
---

# impatience

An advisory by default: it explains, and blocks nothing, on a review reply that reads as impatient: "as I said", "for the last time", "obviously".

## What it catches

A review reply that reads as impatient: "as I said", "for the last time", "obviously".

## Dimension

`stance`: the text reads as a verdict on a person, or as addressed to someone else. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

Ferreira et al. (CSCW 2021) found uncivil features in 66.66% of the non-technical review emails tied to rejected Linux kernel changes, impatience among the most frequent. A phrase that points back at what the writer already said tells the reader they failed to listen. Legitimate urgency reads alike, so it advises.

## Default decision

The code is `PRF8037`. The decision depends on the kind of text judged:

| Kind           | Default |
| -------------- | ------- |
| `review-reply` | advise  |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"impatience": "deny"},
  "paths": {"blog/**": {"impatience": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "impatience", "code": "PRF8037", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/proofread/impatience/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/README.md#findings) - the fields every finding carries
