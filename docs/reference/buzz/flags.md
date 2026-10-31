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

Parse argv against the declared flags, returning {values, lists, positionals, unknown}: switches take no value and record "true", valued flags take the next word or an =value suffix (a repeated valued flag records the last), flags named in repeated take a value each time and collect them in order into lists, the first bare word (one that does not start with a dash) ends the flags and it and every word after it are positionals, verbatim, flags included, the way sudo, env and Go's flag package read argv, and a lone `--` ends the flags too, so a positional that starts with a dash follows it. A dashed word that was not declared, and an empty argument, are returned in unknown rather than guessed at. A flag is declared as it is typed, dashes included ("--file", not "file"), and values and lists are keyed the same way. `magus buzz <file> -- <args>` consumes its own `--`, so a script taking paths needs no second one. Errors when a valued flag is given no value, and when a flag named in required is absent or given an empty value: a workflow passing an unset variable (`--issue "$ISSUE"`) is refused rather than read as a choice.

**Signature:** `flags\parse(argv, switches, valued, [required], [repeated]) -> FlagParse` - [source](https://github.com/egladman/magus/blob/main/std/flags.go)

| Parameter  | Type       | Optional | Description |
| ---------- | ---------- | -------- | ----------- |
| `argv`     | `[]string` |          |             |
| `switches` | `[]string` |          |             |
| `valued`   | `[]string` |          |             |
| `required` | `[]string` | yes      |             |
| `repeated` | `[]string` | yes      |             |

**Returns:** map[string]any

**Example:**

<!-- magus-run -->
```buzz
import "std";
import "flags";

// A flag is declared as it is typed, dashes included: "--file", never "file".
// The first bare word and all after it are positionals.
// `magus buzz <file> -- <args>` consumes its own `--`.
fun main(args: [str]) > void !> any {
    final parsed = flags\parse(["--apply", "--file", "a.go", "--stray", "b.go", "c.go"],
        switches: ["--apply"], valued: ["--file"]);
    std\print(parsed.values["--apply"] ?? "false");
    std\print(parsed.values["--file"] ?? "");
    std\print(parsed.positionals.join(" "));
    std\print(parsed.unknown.join(" "));
    // -> true
    // -> a.go
    // -> b.go c.go
    // -> --stray
}
```

