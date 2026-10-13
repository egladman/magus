---
title: "deferral-no-link"
description: "A rule that is off by default: it reports a review reply deferring to later, a follow-up or a TODO, with no issue link or number once a decisions table turns it on."
tags: [proofread, rules, deferral-no-link, off]
aliases: [reference/prose/deferral-no-link]
---

# deferral-no-link

A rule that is off by default: it reports a review reply deferring to later, a follow-up or a TODO, with no issue link or number once a decisions table turns it on.

## What it catches

A review reply deferring to later, a follow-up or a TODO, with no issue link or number.

## Dimension

`evidence`: a claim says more or less than what was shown. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

Google's review guide (eng-practices, "Handling pushback") does not accept "I'll clean it up later": follow-ups rarely happen, so the author files a bug and may add a TODO naming it. A reply holding a URL, an issue number (#123) or a tracker key (ABC-123) passes. It is a convention, not a study, so it advises.

## Default decision

The code is `PRF8035`. The decision depends on the kind of text judged:

| Kind           | Default |
| -------------- | ------- |
| `review-reply` | off     |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"deferral-no-link": "deny"},
  "paths": {"blog/**": {"deferral-no-link": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "deferral-no-link", "code": "PRF8035", "decision": "off", "url": "https://eli.gladman.cc/magus/reference/proofread/deferral-no-link/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/README.md#findings) - the fields every finding carries
