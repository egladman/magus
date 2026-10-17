---
title: "compact-symbol-negation"
description: "A rule that is off by default: it reports \"!\" or a Unicode symbol standing in for \"not\" or a logic operator once a decisions table turns it on."
tags: [proofread, rules, compact-symbol-negation, off]
aliases: [reference/prose/compact-symbol-negation]
---

# compact-symbol-negation

A rule that is off by default: it reports "!" or a Unicode symbol standing in for "not" or a logic operator once a decisions table turns it on.

## What it catches

"!" or a Unicode symbol standing in for "not" or a logic operator.

## Dimension

`evidence`: a claim says more or less than what was shown. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

Negated framing was the main failure Elkins and Chun measured (arXiv 2601.09724): some models endorsed an action 80-97% of the time when asked whether it should not be done. MetaGlyph (van Gassen, arXiv 2601.07354) saved 62-81% of tokens with symbols, but Claude Haiku 4.5 read its set-membership symbol right 26% of the time, and the not-equal and therefore signs are 2 tokens each in o200k. Negation stays a word. The measurements are in the machine-text research (research-machine-text.md in the proofread roadmap).

## Default decision

The code is `PRF7013`. The decision depends on the kind of text judged:

| Kind            | Default |
| --------------- | ------- |
| `agent-compact` | off     |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"compact-symbol-negation": "deny"},
  "paths": {"blog/**": {"compact-symbol-negation": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "compact-symbol-negation", "code": "PRF7013", "decision": "off", "url": "https://eli.gladman.cc/magus/reference/proofread/compact-symbol-negation/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
