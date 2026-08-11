---
title: strings module
aliases: [modules/strings]
description: "String helpers Buzz's builtins lack: case conversion, comparison, affix trimming, padding, and splitting into lines or fields."
tags: [strings, module, stdlib, magusfile]
---

# strings

String helpers Buzz's builtins lack: case conversion, comparison, affix trimming, padding, and splitting into lines or fields.

> **Naming convention:** import the module under its bare name (`import "strings"`), reach members with a backslash, and call methods in `camelCase`: `strings\someMethod`.

## Methods

### camelCase

Convert s to camelCase.

**Signature:** `strings\camelCase(s) → string` · [source](https://github.com/egladman/magus/blob/main/std/strings.go#L177)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** string

**Example:**

<!-- magus-run -->
```buzz
import "std";
import "strings";

std\print(strings\camelCase("hello world"));
// -> "helloWorld"
```

### snakeCase

Convert s to snake_case.

**Signature:** `strings\snakeCase(s) → string` · [source](https://github.com/egladman/magus/blob/main/std/strings.go#L182)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** string

**Example:**

<!-- magus-run -->
```buzz
import "std";
import "strings";

std\print(strings\snakeCase("HelloWorld"));
// -> "hello_world"
```

### kebabCase

Convert s to kebab-case.

**Signature:** `strings\kebabCase(s) → string` · [source](https://github.com/egladman/magus/blob/main/std/strings.go#L187)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** string

**Example:**

<!-- magus-run -->
```buzz
import "std";
import "strings";

std\print(strings\kebabCase("MyComponentName"));
// -> "my-component-name"
```

### pascalCase

Convert s to PascalCase.

**Signature:** `strings\pascalCase(s) → string` · [source](https://github.com/egladman/magus/blob/main/std/strings.go#L192)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** string

**Example:**

<!-- magus-run -->
```buzz
import "std";
import "strings";

std\print(strings\pascalCase("user_profile"));
// -> "UserProfile"
```

### capitalize

Uppercase the first rune of s and lowercase the rest.

**Signature:** `strings\capitalize(s) → string` · [source](https://github.com/egladman/magus/blob/main/std/strings.go#L197)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** string

**Example:**

<!-- magus-run -->
```buzz
import "std";
import "strings";

std\print(strings\capitalize("hELLO"));
// -> "Hello"
```

### words

Split s into its constituent words (splitting on case changes, digits, and separators).

**Signature:** `strings\words(s) → []string` · [source](https://github.com/egladman/magus/blob/main/std/strings.go#L202)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** []string

**Example:**

<!-- magus-run -->
```buzz
import "std";
import "strings";

final parts = strings\words("parseHTTPResponse2");
foreach (w in parts) { std\print(w); }
// -> parse
// -> HTTP
// -> Response
// -> 2
```

### ellipsis

Trim s to at most length runes, appending "..." when truncated.

**Signature:** `strings\ellipsis(s, length) → string` · [source](https://github.com/egladman/magus/blob/main/std/strings.go#L207)

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

std\print(strings\ellipsis("the quick brown fox", 12));
// -> "the quick..."
```

### upperFirst

Uppercase the first rune of s, leaving the rest untouched. Unlike capitalize, which lowercases the remainder, this preserves interior casing - the form a label or breadcrumb built from an existing string needs.

**Signature:** `strings\upperFirst(s) → string` · [source](https://github.com/egladman/magus/blob/main/std/strings.go#L212)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** string

### compare

Compare a and b lexicographically by byte, returning -1, 0, or 1. Buzz has no < operator on str, so this is what a list.sort comparator over strings is built from.

**Signature:** `strings\compare(a, b) → int` · [source](https://github.com/egladman/magus/blob/main/std/strings.go#L222)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `a` | `string` |  | |
| `b` | `string` |  | |

**Returns:** int

**Example:**

<!-- magus-run -->
```buzz
import "std";
import "strings";

// Buzz has no `<` on str, so sorting strings means handing list.sort a
// comparator built from compare.
final names = mut ["charlie", "alice", "bob"];
names.sort(fun (a: str, b: str) > bool => strings\compare(a, b) < 0);
std\print(names.join(", "));
// -> "alice, bob, charlie"
```

### contains

Report whether s contains substr.

**Signature:** `strings\contains(s, substr) → bool` · [source](https://github.com/egladman/magus/blob/main/std/strings.go#L227)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |
| `substr` | `string` |  | |

**Returns:** bool

### trimPrefix

Remove prefix from the start of s if present, otherwise return s unchanged. Buzz's str.trim only strips whitespace, so there is no built-in way to drop a known affix.

**Signature:** `strings\trimPrefix(s, prefix) → string` · [source](https://github.com/egladman/magus/blob/main/std/strings.go#L232)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |
| `prefix` | `string` |  | |

**Returns:** string

### trimSuffix

Remove suffix from the end of s if present, otherwise return s unchanged.

**Signature:** `strings\trimSuffix(s, suffix) → string` · [source](https://github.com/egladman/magus/blob/main/std/strings.go#L237)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |
| `suffix` | `string` |  | |

**Returns:** string

### padLeft

Left-pad s with pad until it is length runes wide; s is returned unchanged when already that wide or wider. pad defaults to a space. Zero-padding a number so it sorts lexically is the usual reason.

**Signature:** `strings\padLeft(s, length, [pad]) → string` · [source](https://github.com/egladman/magus/blob/main/std/strings.go#L242)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |
| `length` | `int` |  | |
| `pad` | `string` | yes | |

**Returns:** string

### padRight

Right-pad s with pad until it is length runes wide; s is returned unchanged when already that wide or wider. pad defaults to a space. Aligning a column of output is the usual reason.

**Signature:** `strings\padRight(s, length, [pad]) → string` · [source](https://github.com/egladman/magus/blob/main/std/strings.go#L248)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |
| `length` | `int` |  | |
| `pad` | `string` | yes | |

**Returns:** string

### lines

Split s into lines on \n, tolerating \r\n endings and dropping the trailing empty element a final newline would produce. Reading a command's stdout line by line is what this is for; fs.read_lines is the same shape over a file.

**Signature:** `strings\lines(s) → []string` · [source](https://github.com/egladman/magus/blob/main/std/strings.go#L276)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** []string

### fields

Split s around runs of whitespace, discarding empties. Picking a column out of a tool's version banner is what this is for; str.split(" ") cannot, because it yields an empty element per extra space.

**Signature:** `strings\fields(s) → []string` · [source](https://github.com/egladman/magus/blob/main/std/strings.go#L289)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** []string

### splitN

Split s on sep into at most n pieces, leaving any remaining separators in the final piece; n of -1 means no limit. Parsing a KEY=VALUE line whose value itself contains the separator needs this rather than str.split.

**Signature:** `strings\splitN(s, sep, n) → []string` · [source](https://github.com/egladman/magus/blob/main/std/strings.go#L298)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |
| `sep` | `string` |  | |
| `n` | `int` |  | |

**Returns:** []string

### collapseWs

Fold every run of whitespace in s into a single space and trim the ends, so a multi-line value reads as one clean line.

**Signature:** `strings\collapseWs(s) → string` · [source](https://github.com/egladman/magus/blob/main/std/strings.go#L307)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** string

