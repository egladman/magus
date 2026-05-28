# `fmt`

String formatting (printf-style).

> **Naming convention:** Buzz reaches modules off the `import "magus/extra"` aggregate in `camelCase` (`extra.fmt.someMethod`).

## Methods

### `sprintf`

Format string args into the template using Go printf verbs (e.g. %s, %q). Returns the formatted string.

**Signature:** `extra.fmt.sprintf(format, args...) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `format` | `string` |  | |
| `args` | `string` |  | |

**Returns:** string

