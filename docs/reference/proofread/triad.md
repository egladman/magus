---
title: "triad"
description: "A rule that is off by default: it reports a series of exactly three (\"X, Y, and Z\", three parallel short sentences, three parallel bullets) twice in a text, or once in the lead's first sentence once a decisions table turns it on."
tags: [proofread, rules, triad, off]
aliases: [reference/prose/triad]
---

# triad

A rule that is off by default: it reports a series of exactly three ("X, Y, and Z", three parallel short sentences, three parallel bullets) twice in a text, or once in the lead's first sentence once a decisions table turns it on.

## What it catches

A series of exactly three ("X, Y, and Z", three parallel short sentences, three parallel bullets) twice in a text, or once in the lead's first sentence.

## Dimension

`economy`: words that carry nothing. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

Wikipedia's "Signs of AI writing" lists the rule of three among the habits of generated prose, and MeasureShape already counts the series per 1000 words as a feature a voice profile can compare. One natural series is ordinary writing, so a single one mid-paragraph is left alone; a text with two, or a lead that opens on one, reads as a template. Code spans, paths, flags, ALL_CAPS names, numbers and a list the author numbered are not judged. No firing has been labeled, so it ships off.

## Default decision

The code is `PRF4030`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `change-description` | off     |
| `review-summary`     | off     |
| `review-inline`      | off     |
| `review-reply`       | off     |
| `commit-message`     | off     |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"triad": "deny"},
  "paths": {"blog/**": {"triad": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "triad", "code": "PRF4030", "decision": "off", "url": "https://eli.gladman.cc/magus/reference/proofread/triad/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
