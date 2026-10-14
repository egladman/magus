---
title: "nonspecific"
description: "A rule that is off by default: it reports a top-level review comment that judges or asks for a change and names no code, path, line, example or reason once a decisions table turns it on."
tags: [proofread, rules, nonspecific, off]
aliases: [reference/prose/nonspecific]
---

# nonspecific

A rule that is off by default: it reports a top-level review comment that judges or asks for a change and names no code, path, line, example or reason once a decisions table turns it on.

## What it catches

A top-level review comment that judges or asks for a change and names no code, path, line, example or reason.

## Dimension

`evidence`: a claim says more or less than what was shown. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

Gunawardena et al. (CSCW 2022) define destructive criticism as feedback that is nonspecific and inconsiderate, and over half of their respondents had received it in the past year; Bosu et al. (MSR 2015) found a third of review comments were not useful. Measured 2026-10-10 over the AIDev review comments: 2.92 percent of 39639 written by people and 0.15 percent of 42076 written by bots. It judges top-level comments only: an inline comment (review-inline) sits on the line it judges, which is its referent. The anchor gives the place, not the reason, so an inline bare verdict still lacks the suggestion Gunawardena et al. count, and the rule leaves it alone. Measured 2026-10-11 over the adjudicated labeling round 2: 7.0% precision (3 of 43) before, 27.3% (3 of 11) after. Most firings named what they judged in a noun phrase ("the indentation", "this logic") or by the noun the judgment modifies ("redundant docstrings"), so only a verdict on a bare this, it or that, or on nothing, is reported. The rest sat inline on the line they judge. It advises, and stays off until a fresh labeling round measures it. Labeling round 5 measured 32.5% (13 of 40, lower bound 0.20); seven of its wrong firings named a failure the reader can find ("tests are failing", "CI failed", a command that times out), which is the reason for the change asked, so such a reply is left alone. A retry asked with nothing named ("something went wrong, please try again") is still reported. Many of its other wrong firings judged words the writer quoted. A block quote is the comment the reply answers, its referent as an inline comment's line is, so a reply holding one is left alone, and so is a judgment in single quotes. An apology owning the writer's fault ("my bad"), a fix the writer takes on ("will fix it"), and thanks or a conceded point that asks for nothing judge nobody's work. A reply an automated reviewer marks as auto-generated is not the author's.

## Default decision

The code is `PRF8010`. The decision depends on the kind of text judged:

| Kind             | Default |
| ---------------- | ------- |
| `review-summary` | off     |
| `review-inline`  | off     |
| `review-reply`   | off     |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"nonspecific": "deny"},
  "paths": {"blog/**": {"nonspecific": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "nonspecific", "code": "PRF8010", "decision": "off", "url": "https://eli.gladman.cc/magus/reference/proofread/nonspecific/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
