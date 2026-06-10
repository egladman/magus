# `strings`

Case conversion and word helpers (camel/snake/kebab/Pascal, capitalize, words, ellipsis).

> **Naming convention:** import the module under its bare name (`import "strings"`) and call methods in `camelCase` (`strings.someMethod`).

## Methods

### `camel_case`

Convert s to camelCase.

**Signature:** `strings.camelCase(s) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** string

### `snake_case`

Convert s to snake_case.

**Signature:** `strings.snakeCase(s) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** string

### `kebab_case`

Convert s to kebab-case.

**Signature:** `strings.kebabCase(s) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** string

### `pascal_case`

Convert s to PascalCase.

**Signature:** `strings.pascalCase(s) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** string

### `capitalize`

Uppercase the first rune of s and lowercase the rest.

**Signature:** `strings.capitalize(s) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** string

### `words`

Split s into its constituent words (splitting on case changes, digits, and separators).

**Signature:** `strings.words(s) → []string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** []string

### `ellipsis`

Trim s to at most length runes, appending "..." when truncated.

**Signature:** `strings.ellipsis(s, length) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |
| `length` | `int` |  | |

**Returns:** string

