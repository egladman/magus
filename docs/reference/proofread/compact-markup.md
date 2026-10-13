---
title: "compact-markup"
description: "A rule that is off by default: it reports a bold or italic marker, or a Markdown table, in compact agent text once a decisions table turns it on."
tags: [proofread, rules, compact-markup, off]
aliases: [reference/prose/compact-markup]
---

# compact-markup

A rule that is off by default: it reports a bold or italic marker, or a Markdown table, in compact agent text once a decisions table turns it on.

## What it catches

A bold or italic marker, or a Markdown table, in compact agent text.

## Dimension

`economy`: words that carry nothing. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

Measured in tiktoken o200k: bold plus backticks takes a line from 5 tokens to 8, and the same two facts cost 24 tokens as a Markdown table, 13 as a list and 11 as key: value lines. An agent acts on the words, not their emphasis. The measurements are in the 2026-10-13 machine-text research (research-machine-text.md in the proofread roadmap).

## Default decision

The code is `PRF7011`. The decision depends on the kind of text judged:

| Kind            | Default |
| --------------- | ------- |
| `agent-compact` | off     |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"compact-markup": "deny"},
  "paths": {"blog/**": {"compact-markup": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "compact-markup", "code": "PRF7011", "decision": "off", "url": "https://eli.gladman.cc/magus/reference/proofread/compact-markup/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/README.md#findings) - the fields every finding carries
