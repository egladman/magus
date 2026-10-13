---
title: "staccato"
description: "A rule that is off by default: it reports three or more consecutive sentences of six words or fewer in one paragraph once a decisions table turns it on."
tags: [proofread, rules, staccato, off]
aliases: [reference/prose/staccato]
---

# staccato

A rule that is off by default: it reports three or more consecutive sentences of six words or fewer in one paragraph once a decisions table turns it on.

## What it catches

Three or more consecutive sentences of six words or fewer in one paragraph.

## Dimension

`structure`: the reader has to reconstruct the order or the purpose. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

A run of fragments reads as a drumbeat. At a cap of 4 words the rule found nothing and missed a real run; at 6 it found that run and docs/scope.md alone. It denies in a change description and advises on a page; a guide's steps and agent instructions are short by rule, so it does not judge them. Measured 2026-10-11 over the adjudicated labeling round: 28.2% precision (11 of 39) before, 73.3% (11 of 15) after. Left alone now: quoted words, a title, a paragraph laid out as an HTML list, initials and abbreviations, and a run broken by a link, a label ending in a colon, or a line with no word (a path, an emoji, a log line). It stays off: terse template answers ("Low. Only adding a registration.") still fire.

## Default decision

The code is `PRF4012`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `reference`          | off     |
| `change-description` | off     |
| `issue`              | off     |
| `release-notes`      | off     |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"staccato": "deny"},
  "paths": {"blog/**": {"staccato": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "staccato", "code": "PRF4012", "decision": "off", "url": "https://eli.gladman.cc/magus/reference/proofread/staccato/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/README.md#findings) - the fields every finding carries
