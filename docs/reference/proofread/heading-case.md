---
title: "heading-case"
description: "An advisory by default: it explains, and blocks nothing, on a heading whose every word after the first is capitalized."
tags: [proofread, rules, heading-case, advise]
aliases: [reference/prose/heading-case]
---

# heading-case

An advisory by default: it explains, and blocks nothing, on a heading whose every word after the first is capitalized.

## What it catches

A heading whose every word after the first is capitalized.

## Why

Title Case headings are a generated-writing tell. It advises: a heading of proper nouns keeps lower-case words and passes, and the full sentence-case policy with its exceptions stays a repository's own.

## Default decision

The code is `PRF4013`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `reference`          | advise  |
| `guide`              | advise  |
| `agent-instructions` | advise  |
| `issue`              | advise  |
| `release-notes`      | advise  |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"heading-case": "deny"},
  "paths": {"blog/**": {"heading-case": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "heading-case", "code": "PRF4013", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/proofread/heading-case/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
