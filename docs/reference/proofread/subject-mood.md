---
title: "subject-mood"
description: "A deny rule by default: it refuses a commit subject opening in the past tense, the third person or a gerund (\"added\", \"fixes\", \"making\")."
tags: [proofread, rules, subject-mood, deny]
aliases: [reference/prose/subject-mood]
---

# subject-mood

A deny rule by default: it refuses a commit subject opening in the past tense, the third person or a gerund ("added", "fixes", "making").

## What it catches

A commit subject opening in the past tense, the third person or a gerund ("added", "fixes", "making").

## Dimension

`conventions`: a house or genre convention is broken. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

A subject completes "if applied, this commit will ...", so it opens with the verb in the imperative. The rule reads a closed list of about 30 verbs in their past, third-person and gerund forms, not a tagger, so a plural noun that is also a verb ("changes to the key") is reported; the list is the one hack/policy/commits.buzz already denied. Over the 1729 commit messages on main it found nothing, so it denies at no cost.

## Default decision

The code is `PRF1010`. The decision depends on the kind of text judged:

| Kind             | Default |
| ---------------- | ------- |
| `commit-message` | deny    |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"subject-mood": "advise"},
  "paths": {"blog/**": {"subject-mood": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "subject-mood", "code": "PRF1010", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/subject-mood/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
