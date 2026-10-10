---
title: "dash"
description: "A house-style rule, off until a decisions table turns it on: it reports an em dash, an en dash or a spaced double hyphen in prose."
tags: [prose, rules, dash, off]
---

# dash

A house-style rule, off until a decisions table turns it on: it reports an em dash, an en dash or a spaced double hyphen in prose.

## What it catches

An em dash, an en dash or a spaced double hyphen in prose.

## Why

House style: plain-ASCII typography is one repository's policy, ported from its Buzz lint so the judge alone reproduces it. Off unless a decisions table turns it on.

## Default decision

The code is `PRS5002`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `reference`          | off     |
| `guide`              | off     |
| `change-description` | off     |
| `agent-instructions` | off     |
| `review-reply`       | off     |

House style ships `off`: the rule encodes one repository's conventions, not
a rule of writing a teammate reads. A repository turns it on in its decisions table.

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. The prose judge reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"dash": "deny"},
  "paths": {"blog/**": {"dash": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "dash", "code": "PRS5002", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/prose/dash/"}
```

The judge's `-catalog` flag prints this entry with every other rule's.

## See also

- [All prose rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
