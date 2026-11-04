---
title: buzz module
generated_from: reference/buzz/
aliases: [modules/buzz]
description: Buzz source read as data, through the parser magus runs.
tags: [buzz, module, stdlib, magusfile]
---

# buzz

Buzz source read as data, through the parser magus runs.

> **Naming convention:** import the module under its bare name (`import "buzz"`), reach members with a backslash, and call methods in `camelCase`: `buzz\someMethod`.

## Methods

### parse

Parse the Buzz file at path into {path, error, functions, statements, tests}. It never raises: a file that cannot be read or parsed comes back with error set and empty lists. Functions holds every fun, method and function literal with its extent, its !> error type and the lines of the throw statements it owns; statements holds the top-level statements; tests holds the test blocks. Lines are 1-based. embedded parses the way a magusfile's imports load.

**Signature:** `buzz\parse(path, [embedded]) -> BuzzFile` - [source](https://github.com/egladman/magus/blob/main/std/buzz.go)

| Parameter  | Type     | Optional | Description |
| ---------- | -------- | -------- | ----------- |
| `path`     | `string` |          |             |
| `embedded` | `bool`   | yes      |             |

**Returns:** map[string]any

