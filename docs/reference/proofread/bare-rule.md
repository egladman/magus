---
title: "bare-rule"
description: "A house-style rule, off until a decisions table turns it on: it reports \"rule\" in agent instructions with no mechanism named."
tags: [proofread, rules, bare-rule, off]
aliases: [reference/prose/bare-rule]
---

# bare-rule

A house-style rule, off until a decisions table turns it on: it reports "rule" in agent instructions with no mechanism named.

## What it catches

"rule" in agent instructions with no mechanism named.

## Dimension

`conventions`: a house or genre convention is broken. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

House style: in this repository's skills a rule is only what magus enforces, and the rest is an instruction.

## Default decision

The code is `PRF7003`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `agent-instructions` | off     |

House style ships `off`: the rule encodes one repository's conventions, not
a rule of writing a teammate reads. A repository turns it on in its decisions table.

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"bare-rule": "deny"},
  "paths": {"blog/**": {"bare-rule": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "bare-rule", "code": "PRF7003", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/bare-rule/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
