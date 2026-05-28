# `charm`

Constructors for charm values: RFC 6902 JSON Patches over a target's argv (see docs/charms.md).

> **Naming convention:** Buzz reaches modules off the `import "magus/extra"` aggregate in `camelCase` (`extra.charm.someMethod`).

## Methods

### `append`

Append vals to the end of the argv.

**Signature:** `extra.charm.append(vals) → map[string]any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `vals` | `[]string` |  | |

**Returns:** map[string]any

### `prepend`

Insert vals at the front of the argv, in order.

**Signature:** `extra.charm.prepend(vals) → map[string]any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `vals` | `[]string` |  | |

**Returns:** map[string]any

### `after`

Insert vals immediately after the first argv element equal to anchor.

**Signature:** `extra.charm.after(argv, anchor, vals) → map[string]any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `anchor` | `string` |  | |
| `vals` | `[]string` |  | |

**Returns:** map[string]any

### `before`

Insert vals immediately before the first argv element equal to anchor.

**Signature:** `extra.charm.before(argv, anchor, vals) → map[string]any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `anchor` | `string` |  | |
| `vals` | `[]string` |  | |

**Returns:** map[string]any

### `set`

Replace the first argv element equal to anchor with val.

**Signature:** `extra.charm.set(argv, anchor, val) → map[string]any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `anchor` | `string` |  | |
| `val` | `string` |  | |

**Returns:** map[string]any

### `remove`

Remove the first argv element equal to anchor.

**Signature:** `extra.charm.remove(argv, anchor) → map[string]any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `anchor` | `string` |  | |

**Returns:** map[string]any

### `after_func`

Insert vals after the first argv element for which fn(s) is truthy.

**Signature:** `extra.charm.afterFunc(argv, fn, vals) → map[string]any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `fn` | `Callback` |  | |
| `vals` | `[]string` |  | |

**Returns:** map[string]any

### `before_func`

Insert vals before the first argv element for which fn(s) is truthy.

**Signature:** `extra.charm.beforeFunc(argv, fn, vals) → map[string]any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `fn` | `Callback` |  | |
| `vals` | `[]string` |  | |

**Returns:** map[string]any

### `set_func`

Replace the first argv element for which fn(s) is truthy with val.

**Signature:** `extra.charm.setFunc(argv, fn, val) → map[string]any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `fn` | `Callback` |  | |
| `val` | `string` |  | |

**Returns:** map[string]any

### `remove_func`

Remove the first argv element for which fn(s) is truthy.

**Signature:** `extra.charm.removeFunc(argv, fn) → map[string]any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `fn` | `Callback` |  | |

**Returns:** map[string]any

### `path`

Return the JSON Pointer ("/N") of the first argv element equal to anchor — the index, auto-calculated, for hand-built move/copy/test ops.

**Signature:** `extra.charm.path(argv, anchor) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `anchor` | `string` |  | |

**Returns:** string

### `path_func`

Return the JSON Pointer ("/N") of the first argv element for which fn(s) is truthy.

**Signature:** `extra.charm.pathFunc(argv, fn) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `fn` | `Callback` |  | |

**Returns:** string

### `move`

Move the first argv element equal to anchor to the JSON Pointer to ("/-" end, "/0" front, or charm.path(...)).

**Signature:** `extra.charm.move(argv, anchor, to) → map[string]any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `anchor` | `string` |  | |
| `to` | `string` |  | |

**Returns:** map[string]any

### `move_func`

Move the first argv element for which fn(s) is truthy to the JSON Pointer to.

**Signature:** `extra.charm.moveFunc(argv, fn, to) → map[string]any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `fn` | `Callback` |  | |
| `to` | `string` |  | |

**Returns:** map[string]any

### `copy`

Copy the first argv element equal to anchor to the JSON Pointer to ("/-" end, "/0" front, or charm.path(...)).

**Signature:** `extra.charm.copy(argv, anchor, to) → map[string]any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `anchor` | `string` |  | |
| `to` | `string` |  | |

**Returns:** map[string]any

### `copy_func`

Copy the first argv element for which fn(s) is truthy to the JSON Pointer to.

**Signature:** `extra.charm.copyFunc(argv, fn, to) → map[string]any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `fn` | `Callback` |  | |
| `to` | `string` |  | |

**Returns:** map[string]any

### `test`

Guard: assert the first argv element equal to anchor is still at its position when the patch applies (else the run errors).

**Signature:** `extra.charm.test(argv, anchor) → map[string]any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `anchor` | `string` |  | |

**Returns:** map[string]any

### `test_func`

Guard: assert the first argv element for which fn(s) is truthy is still at its position when the patch applies.

**Signature:** `extra.charm.testFunc(argv, fn) → map[string]any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `fn` | `Callback` |  | |

**Returns:** map[string]any

