---
title: "nominalizations"
description: "An advisory by default: it explains, and blocks nothing, on a text whose verbs are turned into nouns (-tion, -ment, -ity, -ness) at a generated-writing rate."
tags: [proofread, rules, nominalizations, advise]
aliases: [reference/prose/nominalizations]
---

# nominalizations

An advisory by default: it explains, and blocks nothing, on a text whose verbs are turned into nouns (-tion, -ment, -ity, -ness) at a generated-writing rate.

## What it catches

A text whose verbs are turned into nouns (-tion, -ment, -ity, -ness) at a generated-writing rate.

## Why

Reinhart et al. (PNAS 2025) measured GPT-4o using nominalizations at 2.1 times the human rate. A suffix is a guess at a nominalization, so the rule advises over a whole text and never points at one word. Over texts of 200 words or more, measured 2026-10-10: 808 agent pull request bodies from the AIDev set ran 55.2 per 1000 pooled (p50 49.8), and 79 percent run over the cap of 30; this repository's 96 hand-written docs pages ran 12.2 (p90 20.3, 1 percent over) and its 61 commit bodies 12.6 (2 percent over), both written by people and agents together. Review comments ran 14.5 by people and 35.4 by bots, pooled.

## Default decision

The code is `PRF4021`. The decision depends on the kind of text judged:

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
  "rules": {"nominalizations": "deny"},
  "paths": {"blog/**": {"nominalizations": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "nominalizations", "code": "PRF4021", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/proofread/nominalizations/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
