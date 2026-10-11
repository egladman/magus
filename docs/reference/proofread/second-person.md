---
title: "second-person"
description: "A deny rule by default: it refuses we, us, our or ours in a guide, which speaks to the reader as you."
tags: [proofread, rules, second-person, advise]
aliases: [reference/prose/second-person]
---

# second-person

A deny rule by default: it refuses we, us, our or ours in a guide, which speaks to the reader as you.

## What it catches

We, us, our or ours in a guide, which speaks to the reader as you.

## Dimension

`conventions`: a house or genre convention is broken. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

A guide is followed step by step by the person reading it; "we" blurs who acts. A "we" the tense rule already reports is left to it, so one word gives one finding. Measured 2026-10-11 over the adjudicated labeling round 2: 18.9% precision (7 of 37) before, 25.0% (7 of 28) after. A heading, a link's text, and a sentence that names the reader beside the project ("you keep the wiring, and we do not force it") or speaks as "I" are left alone. The rest came from READMEs, essays and design pages judged as guides, where "we" names the authors; no guide in this repository fired. It stays off until a fresh labeling round measures it.

## Default decision

The code is `PRF1003`. The decision depends on the kind of text judged:

| Kind    | Default |
| ------- | ------- |
| `guide` | off     |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"second-person": "deny"},
  "paths": {"blog/**": {"second-person": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "second-person", "code": "PRF1003", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/proofread/second-person/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
