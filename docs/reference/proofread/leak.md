---
title: "leak"
description: "A rule that denies on `doc-comment`, `reference`, `guide`, `change-description`, `agent-instructions`, `commit-message`, `issue`, `release-notes`, `changelog`, `cli-help`, and `review-reply` and advises on `agent-reply` by default: it reports residue of a tool or a template: a citation marker or an unfilled placeholder."
tags: [proofread, rules, leak, deny]
aliases: [reference/prose/leak]
---

# leak

A rule that denies on `doc-comment`, `reference`, `guide`, `change-description`, `agent-instructions`, `commit-message`, `issue`, `release-notes`, `changelog`, `cli-help`, and `review-reply` and advises on `agent-reply` by default: it reports residue of a tool or a template: a citation marker or an unfilled placeholder.

## What it catches

Residue of a tool or a template: a citation marker or an unfilled placeholder.

## Dimension

`evidence`: a claim says more or less than what was shown. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

`oaicite`, `[cite: 1]` and `[insert ...]` are unambiguous: no reader is served by them. They show up where text was pasted from a chat, so the rule judges doc comments too. Measured 2026-10-10: 100% precision on its cases (6 of 6), but agent replies' link text naming a file and line (`[describe.go:668]`) read as a `[describe ...]` placeholder, and of 71 AIDev pull request candidates many lenticular-bracket hits were CJK punctuation around a label. A placeholder word now ends at a space, a colon or the bracket, and a lenticular pair counts only around a dagger. Firing rates after are measured at merge.

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
| `agent-reply`        | advise  |

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
