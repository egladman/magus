---
title: "judgment-as-fact"
description: "A rule that is off by default: it reports a recommendation in a review reply stated as a fact, with no reason given once a decisions table turns it on."
tags: [proofread, rules, judgment-as-fact, advise]
aliases: [reference/prose/judgment-as-fact]
---

# judgment-as-fact

A rule that is off by default: it reports a recommendation in a review reply stated as a fact, with no reason given once a decisions table turns it on.

## What it catches

A recommendation in a review reply stated as a fact, with no reason given.

## Dimension

`stance`: the text reads as a verdict on a person, or as addressed to someone else. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

"This should be a map" leaves the author to guess why; the reason, or a label saying it is the writer's call, invites an answer. It advises: a modal also states requirements. Measured 2026-10-11 over the adjudicated labeling round: 23.8% precision (10 of 42) before, 55.6% (10 of 18) after. A reply of more than one sentence gives its facts beside the recommendation, so only a one-sentence reply is judged; a purpose ("to match the guidelines"), an authority ("per the spec"), "would need", and a modal quoted or in parentheses are left alone. It stays off: a reason joined by "and" still fires.

## Default decision

The code is `PRF8002`. The decision depends on the kind of text judged:

| Kind           | Default |
| -------------- | ------- |
| `review-reply` | off     |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"judgment-as-fact": "deny"},
  "paths": {"blog/**": {"judgment-as-fact": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "judgment-as-fact", "code": "PRF8002", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/proofread/judgment-as-fact/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/README.md#findings) - the fields every finding carries
