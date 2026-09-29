---
title: flags module
generated_from: reference/buzz/
aliases: [modules/flags]
description: "Parse a script's argv against the flags it declares."
tags: [flags, module, stdlib, magusfile]
---

# flags

Parse a script's argv against the flags it declares.

> **Naming convention:** import the module under its bare name (`import "flags"`), reach members with a backslash, and call methods in `camelCase`: `flags\someMethod`.

## Methods

### parse

Parse argv against the declared flags, returning {values, positionals, unknown}: switches take no value and record "true", valued flags take the next word or an =value suffix, everything after `--` is a positional, and every argument that was not declared is returned in unknown rather than guessed at. A flag is declared as it is typed, dashes included ("--file", not "file"), and values is keyed the same way. A bare word before `--` is unknown, not a positional; `magus buzz <file> -- <args>` consumes its own `--`, so a script taking paths is run with a second one. Errors when a valued flag is given no value.

**Signature:** `flags\parse(argv, switches, valued) -> FlagParse` - [source](https://github.com/egladman/magus/blob/main/std/flags.go#L58)

| Parameter  | Type       | Optional | Description |
| ---------- | ---------- | -------- | ----------- |
| `argv`     | `[]string` |          |             |
| `switches` | `[]string` |          |             |
| `valued`   | `[]string` |          |             |

**Returns:** map[string]any

**Example:**

<!-- magus-run -->
```buzz
import "std";
import "flags";

// A flag is declared as it is typed, dashes included: "--file", never "file".
// Only the words after `--` are positionals; a bare word before it is unknown.
// `magus buzz <file> -- <args>` consumes its own `--`, so a script taking paths
// is run as `magus buzz x.buzz -- --file a.go -- b.go c.go`.
try {
    final parsed = flags\parse(["--apply", "--file", "a.go", "stray", "--", "b.go", "c.go"],
        switches: ["--apply"], valued: ["--file"]);
    std\print(parsed.values["--apply"] ?? "false");
    std\print(parsed.values["--file"] ?? "");
    std\print(parsed.positionals.join(" "));
    std\print(parsed.unknown.join(" "));
    // -> true
    // -> a.go
    // -> b.go c.go
    // -> stray
} catch (e) {
    std\print("{e}");
}
```

