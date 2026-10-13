---
title: "compact-costly-abbrev"
description: "A rule that is off by default: it reports an abbreviation that costs more tokens than the word it replaces: k8s, w/, b/c, e.g., i.e., or dropped vowels once a decisions table turns it on."
tags: [proofread, rules, compact-costly-abbrev, off]
aliases: [reference/prose/compact-costly-abbrev]
---

# compact-costly-abbrev

A rule that is off by default: it reports an abbreviation that costs more tokens than the word it replaces: k8s, w/, b/c, e.g., i.e., or dropped vowels once a decisions table turns it on.

## What it catches

An abbreviation that costs more tokens than the word it replaces: k8s, w/, b/c, e.g., i.e., or dropped vowels.

## Dimension

`economy`: words that carry nothing. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

Measured in tiktoken o200k: "k8s" is 3 tokens and "Kubernetes" 1; "w/", "b/c", "e.g." and "i.e." each cost 1 more than the word; "cnfgrtn" is 4, "mssg" 3 and "cmmt" 2, against 1 each for the whole word. Telegraph English (Arbuzov et al., arXiv 2605.04426) ranks the most abbreviated versions last. An abbreviation earns its place only where it replaces a word of 2 or more tokens. The measurements are in the 2026-10-13 machine-text research (research-machine-text.md in the proofread roadmap).

## Default decision

The code is `PRF7012`. The decision depends on the kind of text judged:

| Kind            | Default |
| --------------- | ------- |
| `agent-compact` | off     |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"compact-costly-abbrev": "deny"},
  "paths": {"blog/**": {"compact-costly-abbrev": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "compact-costly-abbrev", "code": "PRF7012", "decision": "off", "url": "https://eli.gladman.cc/magus/reference/proofread/compact-costly-abbrev/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/README.md#findings) - the fields every finding carries
