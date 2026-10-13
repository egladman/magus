---
title: "compact-line"
description: "A rule that is off by default: it reports a compact line over 21 words that joins clauses with a semicolon or a comma and a conjunction once a decisions table turns it on."
tags: [proofread, rules, compact-line, off]
aliases: [reference/prose/compact-line]
---

# compact-line

A rule that is off by default: it reports a compact line over 21 words that joins clauses with a semicolon or a comma and a conjunction once a decisions table turns it on.

## What it catches

A compact line over 21 words that joins clauses with a semicolon or a comma and a conjunction.

## Dimension

`structure`: the reader has to reconstruct the order or the purpose. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

The floor is one fact per line. IFScale (Jaroslawicz et al., arXiv 2507.11538) found the best model follows 68% of 500 instructions, and an instruction packed beside another on one line is the easier one to miss. A line counts as more than one fact when it runs long and joins clauses; the length is the 90th percentile of the 1041 prose lines of the skills rewritten to the floor on 2026-10-13. The measurements are in the 2026-10-13 machine-text research (research-machine-text.md in the proofread roadmap).

## Default decision

The code is `PRF7014`. The decision depends on the kind of text judged:

| Kind            | Default |
| --------------- | ------- |
| `agent-compact` | off     |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"compact-line": "deny"},
  "paths": {"blog/**": {"compact-line": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "compact-line", "code": "PRF7014", "decision": "off", "url": "https://eli.gladman.cc/magus/reference/proofread/compact-line/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
