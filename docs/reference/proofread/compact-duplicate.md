---
title: "compact-duplicate"
description: "A rule that is off by default: it reports a compact line that restates an earlier one: half or more of its content words and every negation, condition, scope and order word the same once a decisions table turns it on."
tags: [proofread, rules, compact-duplicate, off]
aliases: [reference/prose/compact-duplicate]
---

# compact-duplicate

A rule that is off by default: it reports a compact line that restates an earlier one: half or more of its content words and every negation, condition, scope and order word the same once a decisions table turns it on.

## What it catches

A compact line that restates an earlier one: half or more of its content words and every negation, condition, scope and order word the same.

## Dimension

`economy`: words that carry nothing. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

Anthropic's skill guidance asks for the smallest set of high-signal tokens and one term per idea, and a restated line spends its tokens twice. The rewrite to the floor on 2026-10-13 merged lines like these by hand. A line restates another only when its negation, obligation, scope, condition and order words match too, since those are what a merge must keep. Over the 1041 lines that rewrite left, it fires twice, both restatements the merges missed. The measurements are in the 2026-10-13 machine-text research (research-machine-text.md in the proofread roadmap).

## Default decision

The code is `PRF7015`. The decision depends on the kind of text judged:

| Kind            | Default |
| --------------- | ------- |
| `agent-compact` | off     |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"compact-duplicate": "deny"},
  "paths": {"blog/**": {"compact-duplicate": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "compact-duplicate", "code": "PRF7015", "decision": "off", "url": "https://eli.gladman.cc/magus/reference/proofread/compact-duplicate/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
