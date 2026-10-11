---
title: "terms"
description: "A house-style rule, off until a decisions table turns it on: it reports a spelling the glossary replaces (\"sub-agent\")."
tags: [proofread, rules, terms, off]
aliases: [reference/prose/terms]
---

# terms

A house-style rule, off until a decisions table turns it on: it reports a spelling the glossary replaces ("sub-agent").

## What it catches

A spelling the glossary replaces ("sub-agent").

## Why

House style: the glossary is one repository's, so the rule is off unless a decisions table turns it on.

## Default decision

The code is `PRF5001`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `doc-comment`        | off     |
| `reference`          | off     |
| `guide`              | off     |
| `change-description` | off     |
| `agent-instructions` | off     |
| `commit-message`     | off     |
| `issue`              | off     |
| `release-notes`      | off     |
| `changelog`          | off     |
| `cli-help`           | off     |
| `review-reply`       | off     |

House style ships `off`: the rule encodes one repository's conventions, not
a rule of writing a teammate reads. A repository turns it on in its decisions table.

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"terms": "deny"},
  "paths": {"blog/**": {"terms": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "terms", "code": "PRF5001", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/terms/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
