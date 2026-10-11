---
title: "participles"
description: "An advisory by default: it explains, and blocks nothing, on a text that hangs present participial clauses on its sentences at a generated-writing rate."
tags: [proofread, rules, participles, advise]
aliases: [reference/prose/participles]
---

# participles

An advisory by default: it explains, and blocks nothing, on a text that hangs present participial clauses on its sentences at a generated-writing rate.

## What it catches

A text that hangs present participial clauses on its sentences at a generated-writing rate.

## Dimension

`economy`: words that carry nothing. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

Reinhart et al. (PNAS 2025) measured GPT-4o using present participial clauses at 5.3 times the human rate. The rule counts a comma before an -ing word that opens a clause, and an -ing word opening a sentence whose comma closes the clause, per 1000 words, over texts of 200 words or more. Measured 2026-10-10: 808 agent pull request bodies from the AIDev set ran 3.9 per 1000 pooled (p50 3.7), and 30 percent run over the cap of 5; this repository's 96 hand-written docs pages ran 1.5 (p90 3.1, 2 percent over) and its 61 commit bodies 0.5 (none over), both written by people and agents together. Review comments ran 0.6 by people and 2.6 by bots, pooled. The docs are a different genre from a pull request, and the rule cannot tell a participle from a gerund without a tagger, so it advises.

## Default decision

The code is `PRF4020`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `change-description` | advise  |
| `reference`          | advise  |
| `guide`              | advise  |
| `issue`              | advise  |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"participles": "deny"},
  "paths": {"blog/**": {"participles": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "participles", "code": "PRF4020", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/proofread/participles/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
