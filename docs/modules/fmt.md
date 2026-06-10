# `fmt`

String formatting (printf-style).

> **Naming convention:** import the module under its bare name (`import "fmt"`) and call methods in `camelCase` (`fmt.someMethod`).

## Methods

### `sprintf`

Format string args into the template using Go printf verbs (e.g. %s, %q). Returns the formatted string.

**Signature:** `fmt.sprintf(format, args...) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `format` | `string` |  | |
| `args` | `string` |  | |

**Returns:** string

