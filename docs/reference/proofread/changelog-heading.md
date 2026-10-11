---
title: "changelog-heading"
description: "A deny rule by default: it refuses a changelog version heading that is not \"## [version] - date\" or \"## [Unreleased]\"."
tags: [proofread, rules, changelog-heading, deny]
aliases: [reference/prose/changelog-heading]
---

# changelog-heading

A deny rule by default: it refuses a changelog version heading that is not "## [version] - date" or "## [Unreleased]".

## What it catches

A changelog version heading that is not "## [version] - date" or "## [Unreleased]".

## Dimension

`conventions`: a house or genre convention is broken. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

Keep a Changelog heads each release "[version] - date", so a reader and a tool find a release by its number and see when it shipped; "[Unreleased]" carries no date because nothing has shipped. A fragment under changes/unreleased/ has no version heading and is not judged by it.

## Default decision

The code is `PRF1030`. The decision depends on the kind of text judged:

| Kind        | Default |
| ----------- | ------- |
| `changelog` | deny    |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"changelog-heading": "advise"},
  "paths": {"blog/**": {"changelog-heading": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "changelog-heading", "code": "PRF1030", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/changelog-heading/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
