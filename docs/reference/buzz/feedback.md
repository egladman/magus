---
title: feedback module
generated_from: reference/buzz/
aliases: [modules/feedback]
description: "One agent session's guard record (every call the guard judged, with its verdict, served nexts and command shape, plus the subagents it started) and a person's verdicts on the re..."
tags: [feedback, module, stdlib, magusfile]
---

# feedback

One agent session's guard record (every call the guard judged, with its verdict, served nexts and command shape, plus the subagents it started) and a person's verdicts on the report's rows, kept per repository.

> **Naming convention:** import the module under its bare name (`import "feedback"`), reach members with a backslash, and call methods in `camelCase`: `feedback\someMethod`.

## Methods

### trail

One session's guard record inside a window: {session, host, since, until, checkouts, observations, spawns}. opts.session picks the session; omitted, it is the session with the newest agent observation in the window. opts.since is a duration back from opts.until (`6h`) or an RFC3339 time, default 24h; opts.until is an RFC3339 time, default now. Reads the trail of every checkout of this repository that changed inside the window, because a session's hooks record into the checkout they ran from, which is rarely the worker's. An unknown option or an unreadable time raises. Reads the workspace on the context; raises MGS1022 outside one.

**Signature:** `feedback\trail([opts]) -> FeedbackTrail` - [source](https://github.com/egladman/magus/blob/main/std/feedback.go#L89)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `opts`    | `map[string]any` | yes      |             |

**Returns:** any

### shape

A shell line with its paths, patterns and literals normalized away, so calls differing only in what they name read alike: `grep -rn foo src` and `grep -rn bar lib` are both `grep -rn <arg>`. Programs, flags, operators and redirections stay. "" for a line the shell parser cannot read.

**Signature:** `feedback\shape(command) -> string` - [source](https://github.com/egladman/magus/blob/main/std/feedback.go#L186)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `command` | `string` |          |             |

**Returns:** string

### shapes

The shape of each program a shell line runs, in order, each with its own redirections: `cd x && grep -rn foo src | head -5` is [`cd <path>`, `grep -rn <arg>`, `head -<n>`]. Programs inside a loop or a command substitution count. Empty for a line the shell parser cannot read.

**Signature:** `feedback\shapes(command) -> []string` - [source](https://github.com/egladman/magus/blob/main/std/feedback.go#L191)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `command` | `string` |          |             |

**Returns:** []string

### marks

Every verdict a person recorded on a feedback row in this repository, oldest first; a later mark on an id supersedes an earlier one. Kept per repository identity, so every checkout reads the same marks. Raises on a store line that does not decode, and MGS1022 outside a workspace.

**Signature:** `feedback\marks() -> [FeedbackMark]` - [source](https://github.com/egladman/magus/blob/main/std/feedback.go#L196)

**Returns:** any

### mark

Record a person's verdict on one feedback row and return it as stored, its time stamped. id is the row's stable id (fb and 12 hex digits), section one of refused, advised, unguarded, next-not-taken, verdict one of should-deny, should-advise, wrong-deny, fine; key names the rule or shape the row groups by. Appends to the per-repository store and rewrites nothing. Raises on a malformed mark, an unwritable store, and MGS1022 outside a workspace.

**Signature:** `feedback\mark(mark) -> FeedbackMark` - [source](https://github.com/egladman/magus/blob/main/std/feedback.go#L205)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `mark`    | `map[string]any` |          |             |

**Returns:** any

