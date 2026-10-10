---
title: "wordy"
description: "A deny rule by default: it refuses a phrase with a shorter equivalent (\"in order to\")."
tags: [prose, rules, wordy, deny]
---

# wordy

A deny rule by default: it refuses a phrase with a shorter equivalent ("in order to").

## What it catches

A phrase with a shorter equivalent ("in order to").

## Why

Each phrase has a shorter spelling that says the same. Over the 261 hand-written docs pages it found four sites, which were fixed.

## Default decision

The code is `PRS4002`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `reference`          | deny    |
| `guide`              | deny    |
| `change-description` | deny    |
| `agent-instructions` | deny    |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. The prose judge reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"wordy": "advise"},
  "paths": {"blog/**": {"wordy": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "wordy", "code": "PRS4002", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/prose/wordy/"}
```

The judge's `-catalog` flag prints this entry with every other rule's.

## See also

- [All prose rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
