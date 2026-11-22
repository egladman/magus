---
title: splice module
generated_from: reference/buzz/
aliases: [modules/splice]
description: "Explicit file edits: plan literal and regex replacements and new files, grade them under the lease, preview the diff and apply the plan."
tags: [splice, module, stdlib, magusfile]
---

# splice

Explicit file edits: plan literal and regex replacements and new files, grade them under the lease, preview the diff and apply the plan.

> **Naming convention:** import the module under its bare name (`import "splice"`), reach members with a backslash, and call methods in `camelCase`: `splice\someMethod`.

## Methods

### replace

A change replacing old with new in path. old must occur exactly once when planned.

**Signature:** `splice\replace(path, old, new) -> EditChange` - [source](https://github.com/egladman/magus/blob/main/std/splice.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `path`    | `string` |          |             |
| `old`     | `string` |          |             |
| `new`     | `string` |          |             |

**Returns:** map[string]any

### replaceAll

A change replacing every occurrence of old with new in path. old must occur at least once when planned. old inside an occurrence of new is not replaced, so `a -> ab` applies once.

**Signature:** `splice\replaceAll(path, old, new) -> EditChange` - [source](https://github.com/egladman/magus/blob/main/std/splice.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `path`    | `string` |          |             |
| `old`     | `string` |          |             |
| `new`     | `string` |          |             |

**Returns:** map[string]any

### replaceRegex

A change replacing every match of the RE2 pattern in path with template, expanded with $1 and ${name}. A pattern that does not compile is a refusal plan reports.

**Signature:** `splice\replaceRegex(path, pattern, template) -> EditChange` - [source](https://github.com/egladman/magus/blob/main/std/splice.go)

| Parameter  | Type     | Optional | Description |
| ---------- | -------- | -------- | ----------- |
| `path`     | `string` |          |             |
| `pattern`  | `string` |          |             |
| `template` | `string` |          |             |

**Returns:** map[string]any

### create

A change creating path holding content. path must not exist when planned, unless it already holds exactly content.

**Signature:** `splice\create(path, content) -> EditChange` - [source](https://github.com/egladman/magus/blob/main/std/splice.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `path`    | `string` |          |             |
| `content` | `string` |          |             |

**Returns:** map[string]any

### blocks

Parse a block document: `## <path>` sections of fenced old, old all, pattern, new and create blocks. name labels its refusals. A document with a refusal carries no changes.

**Signature:** `splice\blocks(text, [name]) -> EditDocument` - [source](https://github.com/egladman/magus/blob/main/std/splice.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `text`    | `string` |          |             |
| `name`    | `string` | yes      |             |

**Returns:** map[string]any

### plan

Resolve changes against the tree and grade every file under the lease, writing nothing. Paths resolve against the working directory as fs paths do. Refusals, and changes the tree already holds, are fields of the plan.

**Signature:** `splice\plan(changes) -> SplicePlan` - [source](https://github.com/egladman/magus/blob/main/std/splice.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `changes` | `any` |          |             |

**Returns:** map[string]any

### diff

Render a plan as a unified diff. The Result carries the complete patch or the refusal. A created file diffs against nothing. context is the unchanged lines around each hunk, 3 by default.

**Signature:** `splice\diff(plan, [context]) -> result\Result::<str>` - [source](https://github.com/egladman/magus/blob/main/std/splice.go)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `plan`    | `map[string]any` |          |             |
| `context` | `int`            | yes      |             |

**Returns:** map[string]any

### apply

Plan the changes again and publish each file. A refusal prevents publication. A write failure triggers restoration of preceding writes and the Result reports any restoration failure. A plan whose every change the tree holds succeeds and writes nothing.

**Signature:** `splice\apply(plan) -> result\Result::<EditApplied>` - [source](https://github.com/egladman/magus/blob/main/std/splice.go)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `plan`    | `map[string]any` |          |             |

**Returns:** map[string]any

