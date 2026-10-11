---
title: "changelog-group"
description: "A rule that denies on `changelog` and advises on `release-notes` by default: it reports a changelog heading that is not Added, Changed, Deprecated, Removed, Fixed or Security."
tags: [proofread, rules, changelog-group, deny]
aliases: [reference/prose/changelog-group]
---

# changelog-group

A rule that denies on `changelog` and advises on `release-notes` by default: it reports a changelog heading that is not Added, Changed, Deprecated, Removed, Fixed or Security.

## What it catches

A changelog heading that is not Added, Changed, Deprecated, Removed, Fixed or Security.

## Why

The six groups are the ones Keep a Changelog names, and the ones this repository's fragment grammar accepts, so a reader finds a kind of change in the same place in every release. In release notes, which are looser, the rule advises and only for a heading that is a plain synonym ("Bug fixes", "Features").

## Default decision

The code is `PRF1031`. The decision depends on the kind of text judged:

| Kind            | Default |
| --------------- | ------- |
| `release-notes` | advise  |
| `changelog`     | deny    |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"changelog-group": "deny"},
  "paths": {"blog/**": {"changelog-group": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "changelog-group", "code": "PRF1031", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/changelog-group/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
