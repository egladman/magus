# `fmt`

String formatting (printf-style).

> **Naming convention:** Teal/Lua binds each module per-import in `snake_case` (`local fmt = require("magus.extra.fmt")`, then `fmt.some_method`). Buzz reaches them off the `import "magus/extra"` aggregate in `camelCase` (`extra.fmt.someMethod`).

## Methods

### `sprintf`

Format string args into the template using Go printf verbs (e.g. %s, %q). Returns the formatted string.

**Signature (Teal):** `fmt.sprintf(format, args...) → string`

**Signature (Buzz):** `extra.fmt.sprintf(format, args...) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `format` | `string` |  | |
| `args` | `string` |  | |

**Returns:** string

