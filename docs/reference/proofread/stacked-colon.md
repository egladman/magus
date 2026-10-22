---
title: "stacked-colon"
description: "A rule that is off by default: it reports two or more clause-introducing colons in one paragraph once a decisions table turns it on."
tags: [proofread, rules, stacked-colon, off]
aliases: [reference/prose/stacked-colon]
---

# stacked-colon

A rule that is off by default: it reports two or more clause-introducing colons in one paragraph once a decisions table turns it on.

## What it catches

Two or more clause-introducing colons in one paragraph.

## Dimension

`economy`: words that carry nothing. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

A colon that introduces a clause reads as a label on the next idea; a second one in the same paragraph turns the prose into a run of labeled claims, a shape generated text takes and a person writing to a colleague rarely does. A leading label of three words or fewer ("Not done:"), a colon inside a code span, a quote or a URL, and a time are not counted. No firing has been labeled, so it ships off.

## Default decision

The code is `PRF4031`. The decision depends on the kind of text judged:

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
  "rules": {"stacked-colon": "deny"},
  "paths": {"blog/**": {"stacked-colon": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "stacked-colon", "code": "PRF4031", "decision": "off", "url": "https://eli.gladman.cc/magus/reference/proofread/stacked-colon/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
