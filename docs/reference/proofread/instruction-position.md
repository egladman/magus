---
title: "instruction-position"
description: "A rule that is off by default: it reports a full skill form of 1500 words or more whose first \"must\" or \"never\" sits past the middle of its body once a decisions table turns it on."
tags: [proofread, rules, instruction-position, off]
aliases: [reference/prose/instruction-position]
---

# instruction-position

A rule that is off by default: it reports a full skill form of 1500 words or more whose first "must" or "never" sits past the middle of its body once a decisions table turns it on.

## What it catches

A full skill form of 1500 words or more whose first "must" or "never" sits past the middle of its body.

## Dimension

`structure`: the reader has to reconstruct the order or the purpose. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

"Lost in the Middle" (Liu et al., TACL 2023) measured models using what sits at the start and the end of a long context better than the middle, from its shortest setting of about 2000 tokens up. IFScale (Jaroslawicz et al., arXiv 2507.11538) found frontier models favor early instructions only at very high instruction counts, so the rule judges the full form alone, the copy written for less capable models. Long is that 2000 tokens, about 1500 words at 1.3 tokens a word, near the median of the 16 full forms magus ships (479 to 4052 words, p50 1577).

## Default decision

The code is `PRF7021`. The decision depends on the kind of text judged:

| Kind             | Default |
| ---------------- | ------- |
| `skill-template` | off     |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"instruction-position": "deny"},
  "paths": {"blog/**": {"instruction-position": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "instruction-position", "code": "PRF7021", "decision": "off", "url": "https://eli.gladman.cc/magus/reference/proofread/instruction-position/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/README.md#findings) - the fields every finding carries
