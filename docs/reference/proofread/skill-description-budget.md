---
title: "skill-description-budget"
description: "A rule that is off by default: it reports a skill description over 100 words, or a full form's description sharing 75% or more of its content words with the short form's once a decisions table turns it on."
tags: [proofread, rules, skill-description-budget, off]
aliases: [reference/prose/skill-description-budget]
---

# skill-description-budget

A rule that is off by default: it reports a skill description over 100 words, or a full form's description sharing 75% or more of its content words with the short form's once a decisions table turns it on.

## What it catches

A skill description over 100 words, or a full form's description sharing 75% or more of its content words with the short form's.

## Dimension

`economy`: words that carry nothing. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

A host lists every installed skill by its description, so each one loads into every session whether or not the skill is used, and a full twin's loads beside its short sibling's. The word budget is the 75th percentile of the authored descriptions of the skills magus ships, counted in words, so the longest quarter advises. A twin that restates its sibling's description loads the same words twice. The repeat line was set by comparing, over the same skills, the content words a twin's own line shares with its short form's against those a restated description plus a sentence shares; it sits between the two, so the first passes and the second advises.

## Default decision

The code is `PRF7020`. The decision depends on the kind of text judged:

| Kind             | Default |
| ---------------- | ------- |
| `skill-template` | off     |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"skill-description-budget": "deny"},
  "paths": {"blog/**": {"skill-description-budget": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "skill-description-budget", "code": "PRF7020", "decision": "off", "url": "https://eli.gladman.cc/magus/reference/proofread/skill-description-budget/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
