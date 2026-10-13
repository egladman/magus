---
title: "decline-no-reason"
description: "A rule that is off by default: it reports a short review reply declining the change (\"won't fix\", \"No, ...\") with no reason once a decisions table turns it on."
tags: [proofread, rules, decline-no-reason, off]
aliases: [reference/prose/decline-no-reason]
---

# decline-no-reason

A rule that is off by default: it reports a short review reply declining the change ("won't fix", "No, ...") with no reason once a decisions table turns it on.

## What it catches

A short review reply declining the change ("won't fix", "No, ...") with no reason.

## Dimension

`stance`: the text reads as a verdict on a person, or as addressed to someone else. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

Google's review guide (eng-practices, "Handling pushback") asks whoever disagrees to explain, and acknowledge the other side's point. Only a short decline is judged: a reply of one paragraph whose words after the decline number five or fewer and hold no reason marker (because, since, so, a colon). A longer "No, the caller already sorts" carries its reason without a marker. It is a convention, not a study, so it advises.

## Default decision

The code is `PRF8036`. The decision depends on the kind of text judged:

| Kind           | Default |
| -------------- | ------- |
| `review-reply` | off     |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"decline-no-reason": "deny"},
  "paths": {"blog/**": {"decline-no-reason": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "decline-no-reason", "code": "PRF8036", "decision": "off", "url": "https://eli.gladman.cc/magus/reference/proofread/decline-no-reason/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
