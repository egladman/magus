---
title: "wordy"
description: "An advisory by default: it explains, and blocks nothing, on a phrase with a shorter equivalent (\"in order to\")."
tags: [proofread, rules, wordy, advise]
aliases: [reference/prose/wordy]
---

# wordy

An advisory by default: it explains, and blocks nothing, on a phrase with a shorter equivalent ("in order to").

## What it catches

A phrase with a shorter equivalent ("in order to").

## Dimension

`economy`: words that carry nothing. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

Each phrase has a shorter spelling that says the same. Over the 261 hand-written docs pages it found four sites, which were fixed.

## Default decision

The code is `PRF4002`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `reference`          | advise  |
| `principle`          | advise  |
| `guide`              | advise  |
| `change-description` | advise  |
| `agent-brief`        | advise  |
| `commit-message`     | advise  |
| `issue`              | advise  |
| `release-notes`      | advise  |
| `changelog`          | advise  |
| `agent-reply`        | advise  |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"wordy": "deny"},
  "paths": {"blog/**": {"wordy": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "wordy", "code": "PRF4002", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/proofread/wordy/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
