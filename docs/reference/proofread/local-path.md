---
title: "local-path"
description: "A house-style rule, off until a decisions table turns it on: it reports a path only the author's machine holds: a home directory, `$HOME`, a temp directory or a drive letter."
tags: [proofread, rules, local-path, off]
aliases: [reference/prose/local-path]
---

# local-path

A house-style rule, off until a decisions table turns it on: it reports a path only the author's machine holds: a home directory, `$HOME`, a temp directory or a drive letter.

## What it catches

A path only the author's machine holds: a home directory, `$HOME`, a temp directory or a drive letter.

## Dimension

`conventions`: a house or genre convention is broken. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

House style: text a repository publishes names a file by its path from the repository root, which resolves in every clone. A path like `~/Repos/<repo>/...` or `/Users/<name>/...` leaks the author's machine and resolves nowhere else. URLs, link targets and `/dev/null` are left alone, and a reply to the person at that machine is not judged.

## Default decision

The code is `PRF5008`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `change-description` | off     |
| `commit-message`     | off     |
| `issue`              | off     |
| `release-notes`      | off     |
| `changelog`          | off     |
| `review-summary`     | off     |
| `review-inline`      | off     |
| `review-reply`       | off     |

House style ships `off`: the rule encodes one repository's conventions, not
a rule of plain writing. A repository turns it on in its decisions table.

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"local-path": "deny"},
  "paths": {"blog/**": {"local-path": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "local-path", "code": "PRF5008", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/local-path/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
