---
title: strings module
aliases: [modules/strings]
description: Case conversion and word helpers (camel/snake/kebab/Pascal, capitalize, words, ellipsis).
tags: [strings, module, stdlib, magusfile]
---

# strings

Case conversion and word helpers (camel/snake/kebab/Pascal, capitalize, words, ellipsis).

> **Naming convention:** import the module under its bare name (`import "strings"`) and call methods in `camelCase` (`strings.someMethod`).

## Methods

### camel_case

Convert s to camelCase.

**Signature:** `strings.camelCase(s) → string` · [source](https://github.com/egladman/magus/blob/main/std/strings.go#L77)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** string

**Example:**

<!-- magus-run -->
```buzz
import "std";
import "strings";

std.print(strings.camelCase("hello world"));
// -> "helloWorld"
```

### snake_case

Convert s to snake_case.

**Signature:** `strings.snakeCase(s) → string` · [source](https://github.com/egladman/magus/blob/main/std/strings.go#L82)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** string

**Example:**

<!-- magus-run -->
```buzz
import "std";
import "strings";

std.print(strings.snakeCase("HelloWorld"));
// -> "hello_world"
```

### kebab_case

Convert s to kebab-case.

**Signature:** `strings.kebabCase(s) → string` · [source](https://github.com/egladman/magus/blob/main/std/strings.go#L87)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** string

**Example:**

<!-- magus-run -->
```buzz
import "std";
import "strings";

std.print(strings.kebabCase("MyComponentName"));
// -> "my-component-name"
```

### pascal_case

Convert s to PascalCase.

**Signature:** `strings.pascalCase(s) → string` · [source](https://github.com/egladman/magus/blob/main/std/strings.go#L92)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** string

**Example:**

<!-- magus-run -->
```buzz
import "std";
import "strings";

std.print(strings.pascalCase("user_profile"));
// -> "UserProfile"
```

### capitalize

Uppercase the first rune of s and lowercase the rest.

**Signature:** `strings.capitalize(s) → string` · [source](https://github.com/egladman/magus/blob/main/std/strings.go#L97)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** string

**Example:**

<!-- magus-run -->
```buzz
import "std";
import "strings";

std.print(strings.capitalize("hELLO"));
// -> "Hello"
```

### words

Split s into its constituent words (splitting on case changes, digits, and separators).

**Signature:** `strings.words(s) → []string` · [source](https://github.com/egladman/magus/blob/main/std/strings.go#L102)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** []string

**Example:**

<!-- magus-run -->
```buzz
import "std";
import "strings";

final parts = strings.words("parseHTTPResponse2");
foreach (w in parts) { std.print(w); }
// -> parse
// -> HTTP
// -> Response
// -> 2
```

### ellipsis

Trim s to at most length runes, appending "..." when truncated.

**Signature:** `strings.ellipsis(s, length) → string` · [source](https://github.com/egladman/magus/blob/main/std/strings.go#L107)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |
| `length` | `int` |  | |

**Returns:** string

**Example:**

<!-- magus-run -->
```buzz
import "std";
import "strings";

std.print(strings.ellipsis("the quick brown fox", 12));
// -> "the quick..."
```

