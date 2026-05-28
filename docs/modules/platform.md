# `platform`

Normalize OS/architecture identifiers across naming conventions (aarch64↔arm64, Darwin↔darwin).

> **Naming convention:** Buzz reaches modules off the `import "magus/extra"` aggregate in `camelCase` (`extra.platform.someMethod`).

## Methods

### `arch`

Normalize an architecture identifier (x86_64, aarch64, armv7l, …) to canonical Go GOARCH (amd64, arm64, arm). With style, render that result in a convention (go|uname); raises on an unknown style. Returns "" when the identifier is unrecognized.

**Signature:** `extra.platform.arch(name, [style]) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `name` | `string` |  | |
| `style` | `string` | yes | |

**Returns:** string

### `os`

Normalize an OS identifier (Darwin, macOS, win, …) to canonical Go GOOS (darwin, windows). With style, render that result in a convention (go|uname); raises on an unknown style. Returns "" when the identifier is unrecognized.

**Signature:** `extra.platform.os(name, [style]) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `name` | `string` |  | |
| `style` | `string` | yes | |

**Returns:** string

