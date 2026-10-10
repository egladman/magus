---
title: "closer"
description: "A deny rule by default: it refuses a sentence that opens by announcing it restates the text above (\"In conclusion,\")."
tags: [proofread, rules, closer, deny]
aliases: [reference/prose/closer]
---

# closer

A deny rule by default: it refuses a sentence that opens by announcing it restates the text above ("In conclusion,").

## What it catches

A sentence that opens by announcing it restates the text above ("In conclusion,").

## Why

A summary of a page the reader just read costs a paragraph and adds nothing. Zero hits over the docs pages, changelog fragments and merged pull requests.

## Default decision

The code is `PRF4009`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `reference`          | deny    |
| `guide`              | deny    |
| `change-description` | deny    |
| `agent-instructions` | deny    |
| `review-reply`       | deny    |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"closer": "advise"},
  "paths": {"blog/**": {"closer": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "closer", "code": "PRF4009", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/closer/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
