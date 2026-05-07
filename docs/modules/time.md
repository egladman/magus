# `time`

Timestamp formatting/parsing and duration parsing (Go time, UTC).

> **Naming convention:** Teal/Lua binds each module per-import in `snake_case` (`local time = require("magus.extra.time")`, then `time.some_method`). Buzz reaches them off the `import "magus/extra"` aggregate in `camelCase` (`extra.time.someMethod`).

## Methods

### `format`

Render Unix-millis as a string using a Go reference layout (UTC).

**Signature (Teal):** `time.format(layout, unix_millis) → string`

**Signature (Buzz):** `extra.time.format(layout, unix_millis) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `layout` | `string` |  | |
| `unix_millis` | `float64` |  | |

**Returns:** string

### `parse`

Parse a string with a Go reference layout into Unix-millis (UTC); errors on mismatch.

**Signature (Teal):** `time.parse(layout, value) → float64`

**Signature (Buzz):** `extra.time.parse(layout, value) → float64`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `layout` | `string` |  | |
| `value` | `string` |  | |

**Returns:** float64

### `parse_duration`

Parse a Go duration string (e.g. "168h", "1h30m") into milliseconds; errors on mismatch.

**Signature (Teal):** `time.parse_duration(duration) → float64`

**Signature (Buzz):** `extra.time.parseDuration(duration) → float64`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `duration` | `string` |  | |

**Returns:** float64

