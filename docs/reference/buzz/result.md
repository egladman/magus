---
title: result module
generated_from: reference/buzz/
aliases: [modules/result]
description: "A value or the Diagnostic that stopped it: Result, the Buzz form of types.Result, with ok, fail, and caught for what a catch hands back."
tags: [result, module, stdlib, magusfile]
---

# result

A value or the Diagnostic that stopped it: Result, the Buzz form of types.Result, with ok, fail, and caught for what a catch hands back.

> **Naming convention:** import the module under its bare name (`import "result"`), reach members with a backslash, and call methods in `camelCase`: `result\someMethod`.

## Methods

### ok

ok is a Result holding value.

**Signature:** `result\ok(value) -> Result::<T>`

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `value`   | `<invalid>` |          |             |

**Returns:** <invalid>

### fail

fail is a Result carrying message and no code. A failure with a code

**Signature:** `result\fail(message) -> Result::<T>`

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `message` | `<invalid>` |          |             |

**Returns:** <invalid>

### caught

caught is a Result carrying what a catch handed back: a str, a Diagnostic,

**Signature:** `result\caught(err, [doing]) -> Result::<T>`

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `err`     | `<invalid>` |          |             |
| `doing`   | `<invalid>` | yes      |             |

**Returns:** <invalid>

