---
title: "leak"
description: "A deny rule by default: it refuses residue of a tool or a template: a citation marker or an unfilled placeholder."
tags: [proofread, rules, leak, deny]
aliases: [reference/prose/leak]
---

# leak

A deny rule by default: it refuses residue of a tool or a template: a citation marker or an unfilled placeholder.

## What it catches

Residue of a tool or a template: a citation marker or an unfilled placeholder.

## Why

`oaicite`, `[cite: 1]` and `[insert ...]` are unambiguous: no reader is served by them. They show up where text was pasted from a chat, so the rule judges doc comments too.

## Default decision

The code is `PRF4005`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `doc-comment`        | deny    |
| `reference`          | deny    |
| `guide`              | deny    |
| `change-description` | deny    |
| `agent-instructions` | deny    |
| `commit-message`     | deny    |
| `issue`              | deny    |
| `release-notes`      | deny    |
| `changelog`          | deny    |
| `cli-help`           | deny    |
| `review-reply`       | deny    |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"leak": "advise"},
  "paths": {"blog/**": {"leak": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "leak", "code": "PRF4005", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/leak/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
