---
title: "history"
description: "A house-style rule, off until a decisions table turns it on: it reports a doc comment phrase narrating the change rather than the code (\"used to\")."
tags: [prose, rules, history, off]
---

# history

A house-style rule, off until a decisions table turns it on: it reports a doc comment phrase narrating the change rather than the code ("used to").

## What it catches

A doc comment phrase narrating the change rather than the code ("used to").

## Why

House style: a comment describes the code as it stands and leaves its history to version control. A doc with a TODO, FIXME, compat or Deprecated marker is exempt.

## Default decision

The code is `PRS6005`. The decision depends on the kind of text judged:

| Kind          | Default |
| ------------- | ------- |
| `doc-comment` | off     |

House style ships `off`: the rule encodes one repository's conventions, not
a rule of writing a teammate reads. A repository turns it on in its decisions table.

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. The prose judge reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"history": "deny"},
  "paths": {"blog/**": {"history": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "history", "code": "PRS6005", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/prose/history/"}
```

The judge's `-catalog` flag prints this entry with every other rule's.

## See also

- [All prose rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
