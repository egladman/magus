---
title: magus-buzz-write
description: "Write and run Buzz, the language magusfiles, spells, and `magus buzz` scripts are written in."
tags: [agents, skills, magus-buzz-write]
aliases:
  - reference/skills/magus-buzz
skill_full_bytes: 8168
skill_simple_bytes: 6770
---

# magus-buzz-write

Write and run Buzz, the language magusfiles, spells, and `magus buzz` scripts are written in. Use when writing or debugging a magusfile target, a spell, or a .buzz file, and when a one-off script is needed in a magus workspace - Buzz is already installed with the whole magus host surface (fs, http, json, yaml, template, vcs, ...), so it needs no dependency install. Also use when Buzz syntax surprises you: namespace access is a backslash, object literals use `=`, and `magus buzz` runs upstream-strict (no top-level control flow, every argument after the first must be labeled).

Install it, rather than copying from this page:

```sh
magus agent install .claude/skills   # writes both forms below
```

An installed copy carries a provenance stamp, so `magus graph verify` can tell you when a magus upgrade has made it stale. Text copied from this page carries none.

## What an installed copy carries

`magus agent install` writes this frontmatter above the body. `magus graph verify` reads it to report whether your installed skills are current.

| field | value |
| --- | --- |
| `license` | `GPL-3.0-or-later` |
| `compatibility` | `any-agent` |
| `source` | `magus` |
| `agent-skill-version` | `37` |
| `knowledge-schema-version` | `9` |
| `skill-content` | `3ce451d61f54` |
| `skill-variant` | `full` |

The `skill-content` digest is shared by both permutations below, so they version together: a magus upgrade makes both stale at once, never one silently.

## Full form

Every mechanical step spelled out, plus the rationale for each. Installed as the `<name>-full` twin: loaded by name rather than always, so a reader who needs the long form can ask for it without every session carrying it.

````markdown
# Writing Buzz

Buzz is the language magusfiles and spells are written in, and `magus buzz` runs
it as a general-purpose scripting language with the whole magus host surface
attached. In a magus workspace it is the right reach for a one-off script -
scanning files, reshaping JSON/YAML/TOML, templating, hitting HTTP - because it
is already installed, it needs no dependency install or virtualenv, and it is
the same language the workspace's own build logic is written in.

## The smallest thing that runs

Everything is imported by bare name, work goes in a function, and you call it.
That skeleton plus `magus describe module` covers most scripts:

```buzz
import "std";

fun main() > void {
    std\print("hello");
}
main();
```

```sh
magus buzz hello.buzz
```

## Run it

| form | use |
| --- | --- |
| `magus buzz <file>` | run a script file |
| `magus buzz -e '<code>'` | run a snippet inline |
| `echo '<code>' \| magus buzz -` | run from stdin (a pipe or heredoc also works with no `-`) |
| `magus buzz -t <file>` | run the file's `test "..." {}` blocks and report pass/fail |
| `magus buzz` (a terminal, no args) | REPL, with the magusfile at cwd loaded |

## Never guess an API: ask

The stdlib is discoverable, and guessing at it is the single biggest source of
wasted turns. `strings` is case-conversion helpers, NOT Go's strings; JSON is
`json\stringify` / `json\parse`, not `encode` / `decode`. Look it up:

```sh
magus describe modules -o name        # every module available to a script
magus describe module json            # its methods, docs, and SIGNATURES with return types
```

This file teaches the fundamentals and nothing more. Escalate deliberately:

| question | where |
| --- | --- |
| what modules exist, what a method takes and RETURNS | `magus describe module <name>` - the authority, generated from the bindings |
| how a feature works, concepts, guides, worked examples | the magus-docs-lookup skill - the documentation is written and searchable |
| what THIS workspace declares (targets, spells, projects) | the magus-query skill |

Anything of substance - error sets, fibers, generics, the full stdlib, sandbox
behavior - is documented; search it rather than guessing from this page.

WRONG: assume `strings\toLower(s)` or `json\encode(v)` exist.
CORRECT: `magus describe module strings`, then write what it lists.

Text operations that are not case conversion are usually METHODS on the value,
not module functions: `"a.buzz".endsWith(".buzz")`, `s.len()`, `list.join(" ")`.

## Imports

Every module, including the Buzz stdlib, must be imported by BARE name. There is
no `magus:` or `buzz:` prefix on the host modules.

```buzz
import "std";                 // print, assert
import "fs"; import "json";   // host modules
```

Available in `magus buzz`: the Buzz stdlib plus `archive`, `charm`, `crypto`,
`encoding`, `env`, `fmt`, `fs`, `http`, `json`, `markdown`, `magus`, `os`,
`path`, `platform`, `semver`, `strings`, `template`, `time`, `toml`, `uuid`,
`vcs`, `xml`, `yaml`.

### Calling magus from a script

`import "magus"` works in a script. Ask magus about the workspace through it
rather than shelling out to the binary - it is in-process, version-pinned, and
has no arg-quoting to get wrong:

```buzz
import "std"; import "magus";

fun main() > void {
    // opts.quiet captures the output instead of echoing it
    final res = magus\describe(["file", "MAGUS.md", "-o", "json"], opts: {"quiet": true});
    std\print(res.stdout);
}
main();
```

WRONG: `proc\exec("magus", args: [...], dir: ".", opts: {})` - magus warns on it.
CORRECT: `magus\cmd`, or the typed `magus\run` / `describe` / `insight` / `doctor`.

Members that need a magusfile raise MGS1022 naming the constraint: the ones
that declare into a workspace being loaded (`magus\project`, the provider
selections) have no script equivalent, and the ones that read a loaded workspace
(`magus\ls`, `targets`, `affected`, `graph`, `where`) are reachable through the
nested commands above.

## Two rules that cause most first-try failures

`magus buzz` runs upstream-strict by default.

1. **Control flow is not allowed at the top level.** Declarations and expression
   statements are; `if`/`while`/`for` are not. Put them in a function and call
   it. `magus buzz --embedded` relaxes this if you want a throwaway snippet.
2. **Every argument after the first must be labeled.**

```buzz
// WRONG (strict mode): argument 2 must be labeled
std\assert(x == 1, "checked x");
template\render(tpl, {"name": "world"});

// CORRECT
std\assert(x == 1, message: "checked x");
template\render(tpl, data: {"name": "world"});
```

## Syntax that differs from what you expect

| thing | Buzz |
| --- | --- |
| namespace access | `fs\list(".")` - a BACKSLASH, not a dot |
| member access | `obj.field`, `"s".len()` - a dot |
| object literal | `Point{ x = 1 }` - `=`, not `:` |
| map literal | `{"key": value}` - `:`, like JSON |
| typed binding | `final n: int = 1;` - type AFTER the name |
| immutable / mutable | `final` / `var`; collections need `mut [1, 2]` to be mutated |
| optional | `int?`, unwrap with `??`, `?.`, or `!` |
| errors | `fun f() > int !> str` declares what it throws; `try`/`catch`, or `expr catch fallback` inline |

Reserved words that cannot be used as binding names (var/fun/param/field/...):
`out`, `from`, `match`, `pat`, `fib`, `rg`, `obj`, `ud`, `zdef`, `typeof`, `type`,
`protocol`, `static`, `extern`, `double`, `any`, `Function`, `int`, `str`, `bool`,
`void` - upstream Buzz's list, kept for parity. `test` is NOT
reserved - every magus target set defines `export fun test(...)`,
the canonical test target, so reserving it would break the CLI's own
model. Prefix or rename only the words above.

A separate hazard: naming a local after a module or a builtin (`map`, `len`, a
module name) SHADOWS it rather than failing to parse, so a later
call through that name hits a non-callable value and dies with a
confusing `null is not callable`. Rename
the local.

A raw string is backticks, and it does NOT interpolate `{...}` - use it for
Mustache templates, regexes, and JSON blobs:

```buzz
template\render(`Hello {{name}}!`, data: {"name": "world"});
```

## A worked script

```buzz
import "std"; import "fs"; import "json"; import "strings";

fun main() > void {
    var count = 0;
    foreach (f in fs\list(".")) {
        if (f.endsWith(".buzz")) { count = count + 1; }
    }
    std\print(json\stringify({"buzz_files": count, "slug": strings\kebabCase("Hello World")}));
}
main();
```

## Test what you write

Buzz has test blocks, and `magus buzz -t` is the runner. Use them for any script
worth keeping, and for spell files (which are Buzz too).

```buzz
import "std"; import "strings";

fun slug(s: str) > str { return strings\kebabCase(s); }

test "slug hyphenates" {
    std\assert(slug("Hello World") == "hello-world", message: "slug");
}
```

```sh
magus buzz -t script.buzz     # ok/fail per block, then a summary line
```

Do not test `magusfile.buzz` itself. It is declarative configuration,
so a test of it tests your configuration, not your logic. Wanting a test
for a magusfile is the signal to move that logic into a spell or a sibling
module and test it there instead.

A module a magusfile imports is tested with the same runner, plus one flag:
`magus buzz -t --embedded render.buzz` - a magusfile's own imports
always parse embedded, not strict, so testing under the strict default would
judge the module by a mode it never actually runs in.

## Where Buzz code belongs

- **A one-off** - a standalone `.buzz` file run with `magus buzz`. Nothing is
  registered; it is a script.
- **Work the workspace repeats** - a target in `magusfile.buzz`, so it gets
  caching, sandboxing, and affected tracking. Targets take
  `(ctx: magus\Context, args: [str])` and receive `magus run <target> -- <args>`
  as that `args` list.
- **A tool adapter** - a spell, so every project of that type gets the ops.

Prefer a target over a script for anything that will be run more than once: a
script re-runs from scratch every time, a target replays from cache.

Reviewing existing Buzz code rather than writing new code: use magus-buzz-review.
````

## Short form

The enumeration dropped, the judgment kept - for the most capable readers, not the least; the bar under the heading above shows by how much. This is the always-loaded primary. Both are hand-authored from one source body; see [Skills](../../guides/integrations/agents/skills.md) for the difference.

<details>
<summary>Show the short form</summary>

````markdown
# Writing Buzz

Buzz is the language magusfiles and spells are written in, and `magus buzz` runs
it as a general-purpose scripting language with the whole magus host surface
attached. Reach for it for a
one-off script: already installed, no dependency install, same language as the
workspace's build logic.

## The smallest thing that runs

Everything is imported by bare name, work goes in a function, and you call it.

```buzz
import "std";

fun main() > void {
    std\print("hello");
}
main();
```

```sh
magus buzz hello.buzz
```

## Run it

| form | use |
| --- | --- |
| `magus buzz <file>` | run a script file |
| `magus buzz -e '<code>'` | run a snippet inline |
| `echo '<code>' \| magus buzz -` | run from stdin (a pipe or heredoc also works with no `-`) |
| `magus buzz -t <file>` | run the file's `test "..." {}` blocks and report pass/fail |
| `magus buzz` (a terminal, no args) | REPL, with the magusfile at cwd loaded |

## Never guess an API: ask

The stdlib is discoverable, and guessing at it is the single biggest source of
wasted turns. Look it up:

```sh
magus describe modules -o name        # every module available to a script
magus describe module json            # its methods, docs, and SIGNATURES with return types
```

Escalate deliberately:

| question | where |
| --- | --- |
| what modules exist, what a method takes and RETURNS | `magus describe module <name>` - the authority, generated from the bindings |
| how a feature works, concepts, guides, worked examples | the magus-docs-lookup skill - the documentation is written and searchable |
| what THIS workspace declares (targets, spells, projects) | the magus-query skill |

Error sets, fibers, generics, the full stdlib and sandbox behavior are all
documented; search rather than guess.

WRONG: assume `strings\toLower(s)` or `json\encode(v)` exist.
CORRECT: `magus describe module strings`, then write what it lists.

Text operations that are not case conversion are usually METHODS on the value,
not module functions: `"a.buzz".endsWith(".buzz")`, `s.len()`, `list.join(" ")`.

## Imports

Every module, including the Buzz stdlib, must be imported by BARE name.

```buzz
import "std";                 // print, assert
import "fs"; import "json";   // host modules
```

Available in `magus buzz`: the Buzz stdlib plus `archive`, `charm`, `crypto`,
`encoding`, `env`, `fmt`, `fs`, `http`, `json`, `markdown`, `magus`, `os`,
`path`, `platform`, `semver`, `strings`, `template`, `time`, `toml`, `uuid`,
`vcs`, `xml`, `yaml`.

### Calling magus from a script

`import "magus"` works in a script. Ask magus about the workspace through it
rather than shelling out to the binary:

```buzz
import "std"; import "magus";

fun main() > void {
    // opts.quiet captures the output instead of echoing it
    final res = magus\describe(["file", "MAGUS.md", "-o", "json"], opts: {"quiet": true});
    std\print(res.stdout);
}
main();
```

WRONG: `proc\exec("magus", args: [...], dir: ".", opts: {})` - magus warns on it.
CORRECT: `magus\cmd`, or the typed `magus\run` / `describe` / `insight` / `doctor`.

Members that need a magusfile raise MGS1022 naming the constraint: the ones
that declare into a workspace being loaded (`magus\project`, the provider
selections) have no script equivalent, and the ones that read a loaded workspace
(`magus\ls`, `targets`, `affected`, `graph`, `where`) are reachable through the
nested commands above.

## Two rules that cause most first-try failures

`magus buzz` runs upstream-strict by default.

1. **Control flow is not allowed at the top level.** Put them in a function and call
   it.
   (`--embedded` relaxes this.)
2. **Every argument after the first must be labeled.**

```buzz
// WRONG (strict mode): argument 2 must be labeled
std\assert(x == 1, "checked x");
template\render(tpl, {"name": "world"});

// CORRECT
std\assert(x == 1, message: "checked x");
template\render(tpl, data: {"name": "world"});
```

## Syntax that differs from what you expect

| thing | Buzz |
| --- | --- |
| namespace access | `fs\list(".")` - a BACKSLASH, not a dot |
| member access | `obj.field`, `"s".len()` - a dot |
| object literal | `Point{ x = 1 }` - `=`, not `:` |
| map literal | `{"key": value}` - `:`, like JSON |
| typed binding | `final n: int = 1;` - type AFTER the name |
| immutable / mutable | `final` / `var`; collections need `mut [1, 2]` to be mutated |
| optional | `int?`, unwrap with `??`, `?.`, or `!` |
| errors | `fun f() > int !> str` declares what it throws; `try`/`catch`, or `expr catch fallback` inline |

Reserved words that cannot be used as binding names (var/fun/param/field/...):
`out`, `from`, `match`, `pat`, `fib`, `rg`, `obj`, `ud`, `zdef`, `typeof`, `type`,
`protocol`, `static`, `extern`, `double`, `any`, `Function`, `int`, `str`, `bool`,
`void`. `test` is NOT
reserved. Prefix or rename only the words above.

A separate hazard: naming a local after a module or a builtin (`map`, `len`, a
module name) SHADOWS it - watch for a confusing `null is not callable`. Rename
the local.

A raw string is backticks, and it does NOT interpolate `{...}`:

```buzz
template\render(`Hello {{name}}!`, data: {"name": "world"});
```

## A worked script

```buzz
import "std"; import "fs"; import "json"; import "strings";

fun main() > void {
    var count = 0;
    foreach (f in fs\list(".")) {
        if (f.endsWith(".buzz")) { count = count + 1; }
    }
    std\print(json\stringify({"buzz_files": count, "slug": strings\kebabCase("Hello World")}));
}
main();
```

## Test what you write

Buzz has test blocks, and `magus buzz -t` is the runner. Use them for any script
worth keeping, and for spell files.

```buzz
import "std"; import "strings";

fun slug(s: str) > str { return strings\kebabCase(s); }

test "slug hyphenates" {
    std\assert(slug("Hello World") == "hello-world", message: "slug");
}
```

```sh
magus buzz -t script.buzz     # ok/fail per block, then a summary line
```

Do not test `magusfile.buzz` itself. Wanting a test
for a magusfile is the signal to move that logic into a spell or a sibling
module and test it there instead.

A module a magusfile imports is tested with the same runner, plus one flag:
`magus buzz -t --embedded render.buzz` (a magusfile's
imports parse embedded, not strict).

## Where Buzz code belongs

- **A one-off** - a standalone `.buzz` file run with `magus buzz`.
- **Work the workspace repeats** - a target in `magusfile.buzz`. Targets take
  `(ctx: magus\Context, args: [str])` and receive `magus run <target> -- <args>`
  as that `args` list.
- **A tool adapter** - a spell, so every project of that type gets the ops.

Prefer a target over a script for anything that will be run more than once.

Reviewing existing Buzz code rather than writing new code: use magus-buzz-review.
````


</details>
