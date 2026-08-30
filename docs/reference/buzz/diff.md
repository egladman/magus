---
title: diff module
generated_from: reference/buzz/
aliases: [modules/diff]
description: Unified line diffs, for reporting what drifted rather than only that something did.
tags: [diff, module, stdlib, magusfile]
---

# diff

Unified line diffs, for reporting what drifted rather than only that something did.

> **Naming convention:** import the module under its bare name (`import "diff"`), reach members with a backslash, and call methods in `camelCase`: `diff\someMethod`.

## Methods

### unified

Return a unified diff of a and b, or "" when they are identical - so the result doubles as the check. from_label and to_label name the two sides in the +++/--- header (default "a" and "b"); pass the file path and something like "regenerated" to make a drift report read like a patch. context is the unchanged lines kept around each hunk, defaulting to 3 as git does.

**Signature:** `diff\unified(a, b, [from_label], [to_label], [context]) -> string` - [source](https://github.com/egladman/magus/blob/main/std/diff.go#L98)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `a` | `string` |  | |
| `b` | `string` |  | |
| `from_label` | `string` | yes | |
| `to_label` | `string` | yes | |
| `context` | `int` | yes | |

**Returns:** string

### equal

Report whether a and b are identical after normalizing line endings and a single trailing newline. It is the comparison a drift gate wants: a file that differs only by CRLF or by whether the last line is newline-terminated has not drifted in any sense a reader cares about, and a bare == would say it had.

**Signature:** `diff\equal(a, b) -> bool` - [source](https://github.com/egladman/magus/blob/main/std/diff.go#L129)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `a` | `string` |  | |
| `b` | `string` |  | |

**Returns:** bool

### stat

Return a one-line summary of the change: "3 added, 1 removed", or "" when a and b are identical. For a report that wants the shape of a drift without the whole patch - a summary line per file, with unified reserved for the one the reader drills into.

**Signature:** `diff\stat(a, b) -> string` - [source](https://github.com/egladman/magus/blob/main/std/diff.go#L134)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `a` | `string` |  | |
| `b` | `string` |  | |

**Returns:** string

