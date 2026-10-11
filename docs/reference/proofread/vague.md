---
title: "vague"
description: "A deny rule by default: it refuses weight or consensus asserted with nothing named (\"experts argue\", \"the stakes are high\")."
tags: [proofread, rules, vague, deny]
aliases: [reference/prose/vague]
---

# vague

A deny rule by default: it refuses weight or consensus asserted with nothing named ("experts argue", "the stakes are high").

## What it catches

Weight or consensus asserted with nothing named ("experts argue", "the stakes are high").

## Dimension

`evidence`: a claim says more or less than what was shown. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

The reader cannot check a source that is not named. Zero hits over the docs pages, changelog fragments and merged pull requests.

## Default decision

The code is `PRF4008`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `reference`          | deny    |
| `guide`              | deny    |
| `change-description` | deny    |
| `agent-instructions` | deny    |
| `commit-message`     | deny    |
| `issue`              | deny    |
| `release-notes`      | deny    |
| `changelog`          | deny    |
| `review-reply`       | deny    |
| `agent-reply`        | deny    |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"vague": "advise"},
  "paths": {"blog/**": {"vague": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "vague", "code": "PRF4008", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/vague/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
