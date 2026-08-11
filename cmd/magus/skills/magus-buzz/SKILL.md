# Writing Buzz

Buzz is the language magusfiles and spells are written in, and `magus buzz` runs
it as a general-purpose scripting language with the whole magus host surface
attached.{{if .Full}} In a magus workspace it is the right reach for a one-off script -
scanning files, reshaping JSON/YAML/TOML, templating, hitting HTTP - because it
is already installed, it needs no dependency install or virtualenv, and it is
the same language the workspace's own build logic is written in.{{else}} Reach for it for a
one-off script: already installed, no dependency install, same language as the
workspace's build logic.{{end}}

## The smallest thing that runs

Everything is imported by bare name, work goes in a function, and you call it.{{if .Full}}
That skeleton plus `magus describe module` covers most scripts:{{end}}

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
wasted turns.{{if .Full}} `strings` is case-conversion helpers, NOT Go's strings; JSON is
`json\stringify` / `json\parse`, not `encode` / `decode`.{{end}} Look it up:

```sh
magus describe modules -o name        # every module available to a script
magus describe module json            # its methods, docs, and SIGNATURES with return types
```

{{if .Full}}This file teaches the fundamentals and nothing more. Escalate deliberately:{{else}}Escalate deliberately:{{end}}

| question | where |
| --- | --- |
| what modules exist, what a method takes and RETURNS | `magus describe module <name>` - the authority, generated from the bindings |
| how a feature works, concepts, guides, worked examples | the magus-docs skill - the documentation is written and searchable |
| what THIS workspace declares (targets, spells, projects) | the magus-query skill |

{{if .Full}}Anything of substance - error sets, fibers, generics, the full stdlib, sandbox
behavior - is documented; search it rather than guessing from this page.{{else}}Error sets, fibers, generics, the full stdlib and sandbox behavior are all
documented; search rather than guess.{{end}}

WRONG: assume `strings\toLower(s)` or `json\encode(v)` exist.
CORRECT: `magus describe module strings`, then write what it lists.

Text operations that are not case conversion are usually METHODS on the value,
not module functions: `"a.buzz".endsWith(".buzz")`, `s.len()`, `list.join(" ")`.

## Imports

Every module, including the Buzz stdlib, must be imported by BARE name.{{if .Full}} There is
no `magus:` or `buzz:` prefix on the host modules.{{end}}

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
rather than shelling out to the binary{{if .Full}} - it is in-process, version-pinned, and
has no arg-quoting to get wrong{{end}}:

```buzz
import "std"; import "magus";

fun main() > void {
    // opts.quiet captures the output instead of echoing it
    final res = magus\describe(["file", "MAGUS.md", "-o", "json"], opts: {"quiet": true});
    std\print(res.stdout);
}
main();
```

WRONG: `os\exec("magus", args: [...], dir: ".", opts: {})` - magus warns on it.
CORRECT: `magus\cmd`, or the typed `magus\run` / `describe` / `insight` / `doctor`.

Members that need a magusfile raise MGS1022 naming the constraint: the ones
that declare into a workspace being loaded (`magus\project`, the provider
selections) have no script equivalent, and the ones that read a loaded workspace
(`magus\ls`, `targets`, `affected`, `graph`, `where`) are reachable through the
nested commands above.

## Two rules that cause most first-try failures

`magus buzz` runs upstream-strict by default.

1. **Control flow is not allowed at the top level.**{{if .Full}} Declarations and expression
   statements are; `if`/`while`/`for` are not.{{end}} Put them in a function and call
   it.{{if .Full}} `magus buzz --embedded` relaxes this if you want a throwaway snippet.{{else}}
   (`--embedded` relaxes this.){{end}}
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
`void`{{if .Full}} - upstream Buzz's list, kept for parity{{end}}. `test` is NOT
reserved{{if .Full}} - every magus target set defines `export fun test(...)`,
the canonical test target, so reserving it would break the CLI's own
model{{end}}. Prefix or rename only the words above.

A separate hazard: naming a local after a module or a builtin (`map`, `len`, a
module name) SHADOWS it{{if .Full}} rather than failing to parse, so a later
call through that name hits a non-callable value and dies with a
confusing{{else}} - watch for a confusing{{end}} `null is not callable`. Rename
the local.

A raw string is backticks, and it does NOT interpolate `{...}`{{if .Full}} - use it for
Mustache templates, regexes, and JSON blobs{{end}}:

```buzz
template\render(`Hello {{"{{name}}"}}!`, data: {"name": "world"});
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
worth keeping, and for spell files{{if .Full}} (which are Buzz too){{end}}.

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

## Where Buzz code belongs

- **A one-off** - a standalone `.buzz` file run with `magus buzz`.{{if .Full}} Nothing is
  registered; it is a script.{{end}}
- **Work the workspace repeats** - a target in `magusfile.buzz`{{if .Full}}, so it gets
  caching, sandboxing, and affected tracking{{end}}. Targets take
  `(ctx: magus\Context, args: [str])` and receive `magus run <target> -- <args>`
  as that `args` list.
- **A tool adapter** - a spell, so every project of that type gets the ops.

Prefer a target over a script for anything that will be run more than once{{if .Full}}: a
script re-runs from scratch every time, a target replays from cache{{end}}.

Reviewing existing Buzz code rather than writing new code: use magus-buzz-review.
