# `magus`

Magus core primitives.

> **Naming convention:** Teal/Lua binds each module per-import in `snake_case` (`local magus = require("magus.extra.magus")`, then `magus.some_method`). Buzz reaches them off the `import "magus/extra"` aggregate in `camelCase` (`extra.magus.someMethod`).

## Methods

### `cmd`

Run the magus binary with args. Output streams live and is captured; returns {stdout, stderr, code, ok}. Raises if the invocation exits non-zero (catch it for non-fatal use).

**Signature (Teal):** `magus.cmd(args) → map[string]any`

**Signature (Buzz):** `extra.magus.cmd(args) → map[string]any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `args` | `[]string` |  | |

**Returns:** map[string]any

### `bust_cache`

Invalidate the build cache. Escape hatch — prefer modeling missing inputs as Sources. No arg clears all; a project path clears one project.

**Signature (Teal):** `magus.bust_cache([project_path])`

**Signature (Buzz):** `extra.magus.bustCache([project_path])`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `project_path` | `string` | yes | |

