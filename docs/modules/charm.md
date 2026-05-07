# `charm`

Constructors for charm values: RFC 6902 JSON Patches over a target's argv (see docs/charms.md).

> **Naming convention:** Teal/Lua binds each module per-import in `snake_case` (`local charm = require("magus.extra.charm")`, then `charm.some_method`). Buzz reaches them off the `import "magus/extra"` aggregate in `camelCase` (`extra.charm.someMethod`).

## Methods

### `append`

Append vals to the end of the argv.

**Signature (Teal):** `charm.append(vals) → map[string]any`

**Signature (Buzz):** `extra.charm.append(vals) → map[string]any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `vals` | `[]string` |  | |

**Returns:** map[string]any

### `prepend`

Insert vals at the front of the argv, in order.

**Signature (Teal):** `charm.prepend(vals) → map[string]any`

**Signature (Buzz):** `extra.charm.prepend(vals) → map[string]any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `vals` | `[]string` |  | |

**Returns:** map[string]any

### `after`

Insert vals immediately after the first argv element equal to anchor.

**Signature (Teal):** `charm.after(argv, anchor, vals) → map[string]any`

**Signature (Buzz):** `extra.charm.after(argv, anchor, vals) → map[string]any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `anchor` | `string` |  | |
| `vals` | `[]string` |  | |

**Returns:** map[string]any

### `before`

Insert vals immediately before the first argv element equal to anchor.

**Signature (Teal):** `charm.before(argv, anchor, vals) → map[string]any`

**Signature (Buzz):** `extra.charm.before(argv, anchor, vals) → map[string]any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `anchor` | `string` |  | |
| `vals` | `[]string` |  | |

**Returns:** map[string]any

### `set`

Replace the first argv element equal to anchor with val.

**Signature (Teal):** `charm.set(argv, anchor, val) → map[string]any`

**Signature (Buzz):** `extra.charm.set(argv, anchor, val) → map[string]any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `anchor` | `string` |  | |
| `val` | `string` |  | |

**Returns:** map[string]any

### `remove`

Remove the first argv element equal to anchor.

**Signature (Teal):** `charm.remove(argv, anchor) → map[string]any`

**Signature (Buzz):** `extra.charm.remove(argv, anchor) → map[string]any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `anchor` | `string` |  | |

**Returns:** map[string]any

### `after_func`

Insert vals after the first argv element for which fn(s) is truthy.

**Signature (Teal):** `charm.after_func(argv, fn, vals) → map[string]any`

**Signature (Buzz):** `extra.charm.afterFunc(argv, fn, vals) → map[string]any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `fn` | `Callback` |  | |
| `vals` | `[]string` |  | |

**Returns:** map[string]any

### `before_func`

Insert vals before the first argv element for which fn(s) is truthy.

**Signature (Teal):** `charm.before_func(argv, fn, vals) → map[string]any`

**Signature (Buzz):** `extra.charm.beforeFunc(argv, fn, vals) → map[string]any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `fn` | `Callback` |  | |
| `vals` | `[]string` |  | |

**Returns:** map[string]any

### `set_func`

Replace the first argv element for which fn(s) is truthy with val.

**Signature (Teal):** `charm.set_func(argv, fn, val) → map[string]any`

**Signature (Buzz):** `extra.charm.setFunc(argv, fn, val) → map[string]any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `fn` | `Callback` |  | |
| `val` | `string` |  | |

**Returns:** map[string]any

### `remove_func`

Remove the first argv element for which fn(s) is truthy.

**Signature (Teal):** `charm.remove_func(argv, fn) → map[string]any`

**Signature (Buzz):** `extra.charm.removeFunc(argv, fn) → map[string]any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `fn` | `Callback` |  | |

**Returns:** map[string]any

### `path`

Return the JSON Pointer ("/N") of the first argv element equal to anchor — the index, auto-calculated, for hand-built move/copy/test ops.

**Signature (Teal):** `charm.path(argv, anchor) → string`

**Signature (Buzz):** `extra.charm.path(argv, anchor) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `anchor` | `string` |  | |

**Returns:** string

### `path_func`

Return the JSON Pointer ("/N") of the first argv element for which fn(s) is truthy.

**Signature (Teal):** `charm.path_func(argv, fn) → string`

**Signature (Buzz):** `extra.charm.pathFunc(argv, fn) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `fn` | `Callback` |  | |

**Returns:** string

### `move`

Move the first argv element equal to anchor to the JSON Pointer to ("/-" end, "/0" front, or charm.path(...)).

**Signature (Teal):** `charm.move(argv, anchor, to) → map[string]any`

**Signature (Buzz):** `extra.charm.move(argv, anchor, to) → map[string]any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `anchor` | `string` |  | |
| `to` | `string` |  | |

**Returns:** map[string]any

### `move_func`

Move the first argv element for which fn(s) is truthy to the JSON Pointer to.

**Signature (Teal):** `charm.move_func(argv, fn, to) → map[string]any`

**Signature (Buzz):** `extra.charm.moveFunc(argv, fn, to) → map[string]any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `fn` | `Callback` |  | |
| `to` | `string` |  | |

**Returns:** map[string]any

### `copy`

Copy the first argv element equal to anchor to the JSON Pointer to ("/-" end, "/0" front, or charm.path(...)).

**Signature (Teal):** `charm.copy(argv, anchor, to) → map[string]any`

**Signature (Buzz):** `extra.charm.copy(argv, anchor, to) → map[string]any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `anchor` | `string` |  | |
| `to` | `string` |  | |

**Returns:** map[string]any

### `copy_func`

Copy the first argv element for which fn(s) is truthy to the JSON Pointer to.

**Signature (Teal):** `charm.copy_func(argv, fn, to) → map[string]any`

**Signature (Buzz):** `extra.charm.copyFunc(argv, fn, to) → map[string]any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `fn` | `Callback` |  | |
| `to` | `string` |  | |

**Returns:** map[string]any

### `test`

Guard: assert the first argv element equal to anchor is still at its position when the patch applies (else the run errors).

**Signature (Teal):** `charm.test(argv, anchor) → map[string]any`

**Signature (Buzz):** `extra.charm.test(argv, anchor) → map[string]any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `anchor` | `string` |  | |

**Returns:** map[string]any

### `test_func`

Guard: assert the first argv element for which fn(s) is truthy is still at its position when the patch applies.

**Signature (Teal):** `charm.test_func(argv, fn) → map[string]any`

**Signature (Buzz):** `extra.charm.testFunc(argv, fn) → map[string]any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `fn` | `Callback` |  | |

**Returns:** map[string]any

