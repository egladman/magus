---
title: comments module
generated_from: reference/buzz/
aliases: [modules/comments]
description: Comment blocks of source files, read with the comment syntax their spells declare.
tags: [comments, module, stdlib, magusfile]
---

# comments

Comment blocks of source files, read with the comment syntax their spells declare.

> **Naming convention:** import the module under its bare name (`import "comments"`), reach members with a backslash, and call methods in `camelCase`: `comments\someMethod`.

## Methods

### blocks

Return the prose of each comment block in paths as [{path, lines: [{line, col, text}]}], in path order. syntax maps a file extension (".go") to the comment syntax a spell declares for it, as magus\describe.spell() reports it; a path whose extension it lacks is skipped. A block is a run of own-line line comments on consecutive lines, one block comment, or one trailing comment; directive comments, blank comment lines and indented code examples are left out. line and col are 1-based, col counted in runes, and text is the source from col on, so a column into text maps back by addition.

**Signature:** `comments\blocks(paths, syntax) -> any` - [source](https://github.com/egladman/magus/blob/main/std/comments.go#L48)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `paths`   | `[]string`       |          |             |
| `syntax`  | `map[string]any` |          |             |

**Returns:** any

