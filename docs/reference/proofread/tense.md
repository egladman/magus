---
title: "tense"
description: "A house-style rule, off until a decisions table turns it on: it reports a claim in the future tense, or a first-person account of a change."
tags: [proofread, rules, tense, off]
aliases: [reference/prose/tense]
---

# tense

A house-style rule, off until a decisions table turns it on: it reports a claim in the future tense, or a first-person account of a change.

## What it catches

A claim in the future tense, or a first-person account of a change.

## Why

House style: this repository describes what the code does, in the present tense, with no author in a description. A team elsewhere writes "we" and "I" in a pull request, so the rule is off unless a decisions table turns it on.

## Default decision

The code is `PRF5004`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `reference`          | off     |
| `guide`              | off     |
| `change-description` | off     |
| `agent-instructions` | off     |
| `commit-message`     | off     |
| `issue`              | off     |
| `release-notes`      | off     |
| `changelog`          | off     |

House style ships `off`: the rule encodes one repository's conventions, not
a rule of writing a teammate reads. A repository turns it on in its decisions table.

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"tense": "deny"},
  "paths": {"blog/**": {"tense": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "tense", "code": "PRF5004", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/tense/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
