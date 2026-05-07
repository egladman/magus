# `platform`

Normalize OS/architecture identifiers across naming conventions (aarch64↔arm64, Darwin↔darwin).

> **Naming convention:** Teal/Lua binds each module per-import in `snake_case` (`local platform = require("magus.extra.platform")`, then `platform.some_method`). Buzz reaches them off the `import "magus/extra"` aggregate in `camelCase` (`extra.platform.someMethod`).

## Methods

### `arch`

Normalize an architecture identifier (x86_64, aarch64, armv7l, …) to canonical Go GOARCH (amd64, arm64, arm). With style, render that result in a convention (go|uname); raises on an unknown style. Returns "" when the identifier is unrecognized.

**Signature (Teal):** `platform.arch(name, [style]) → string`

**Signature (Buzz):** `extra.platform.arch(name, [style]) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `name` | `string` |  | |
| `style` | `string` | yes | |

**Returns:** string

### `os`

Normalize an OS identifier (Darwin, macOS, win, …) to canonical Go GOOS (darwin, windows). With style, render that result in a convention (go|uname); raises on an unknown style. Returns "" when the identifier is unrecognized.

**Signature (Teal):** `platform.os(name, [style]) → string`

**Signature (Buzz):** `extra.platform.os(name, [style]) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `name` | `string` |  | |
| `style` | `string` | yes | |

**Returns:** string

