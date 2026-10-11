---
title: "body-separator"
description: "A deny rule by default: it refuses a commit body that starts on the line after the subject."
tags: [proofread, rules, body-separator, deny]
aliases: [reference/prose/body-separator]
---

# body-separator

A deny rule by default: it refuses a commit body that starts on the line after the subject.

## What it catches

A commit body that starts on the line after the subject.

## Why

Git, and every tool built on it, takes the first paragraph as the subject; with no blank line the body joins it and a one-line log shows both. A message that is a subject and trailers ("Key: value" lines) is left alone. None of the 1729 commit messages on main runs the body into the subject.

## Default decision

The code is `PRF1013`. The decision depends on the kind of text judged:

| Kind             | Default |
| ---------------- | ------- |
| `commit-message` | deny    |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"body-separator": "advise"},
  "paths": {"blog/**": {"body-separator": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "body-separator", "code": "PRF1013", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/body-separator/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
