# Writing Buzz

Buzz is a small, statically typed scripting language. It looks like TypeScript or
Swift, and it is in no model's training data, so code written from habit parses
just often enough to mislead.{{if .Full}} Measured on 42 short programs written the
way a TypeScript, Go or Python author would: the checker rejected 22, and of the 20
it accepted, 6 failed or printed the wrong thing when run, and every one that called
its own `main()` ran twice.{{end}} Work in this order: copy the shapes on this page,
check, fix the first error, run.

## The loop

```sh
{{cmd "buzz"}} --check script.buzz    # parse and type-check: every error, nothing runs
{{cmd "buzz"}} script.buzz a b        # run it: magus calls main(args) for you
{{cmd "buzz"}} -t script.buzz         # run its test "..." {} blocks
```

1. Run `--check` after every edit. Fix the FIRST error, then check again{{if .Full}}:
   a parse error early in a file produces later errors that are only echoes of it{{else}};
   later errors are often echoes of it{{end}}.
2. A clean check is not a working script. The checker does NOT see a method that
   does not exist on a str, list or map, a free function like `len(xs)`, or a write
   into a map declared without `mut`. Those fail only when run, with no line
   number. So run the script, or its tests, before calling it done.
3. `null is not callable` at run time means you called a method or function that
   does not exist. Look it up in [Built-in methods](#built-in-methods).

## One script with every common shape

Copy from this. Every line of it checks and runs.

```buzz
import "std";
import "fs";
import "encoding/json";

object Rect {
    width: int,
    height: int = 1,

    fun area() > int {
        return this.width * this.height;
    }
}

enum Level { low, high }

fun firstEven(numbers: [int]) > int? {
    foreach (n in numbers) {
        if (n % 2 == 0) {
            return n;
        }
    }
    return null;
}

fun parsePort(text: str) > int !> str {
    final port = std\parseInt(text);
    if (port == null or port! < 1) {
        throw "invalid port: {text}";
    }
    return port!;
}

fun main(args: [str]) > void !> any {
    final rect = Rect{ width = 2, height = 3 };
    std\print("area={rect.area()} args={args.len()}");

    final names = mut ["b", "a"];
    names.append("c");
    names.sort(fun (left: str, right: str) > bool => left < right);
    final doubled = [1, 2].map(fun (index: int, n: int) > int => n * 2);

    final counts = mut {"apple": 1};
    counts["pear"] = (counts["pear"] ?? 0) + 1;
    foreach (name, count in counts) {
        std\print("{name}={count}");
    }

    for (var i = 0; i < 3; i = i + 1) {
        if (i == 0) {
            continue;
        } else if (i == 1 and names.len() > 0) {
            std\print("first={names[0]}");
        }
    }

    final label = if (doubled.len() > 1) "many" else "few";
    final found = firstEven([1, 3, 4]);
    if (found -> even) {
        std\print("even={even} label={label}");
    }

    final port = parsePort("x") catch 0;
    try {
        parsePort("-1");
    } catch (err: str) {
        std\print("caught: {err}");
    }

    final doc = json\parse(`{"qty": 2, "tags": ["a"]}`);
    final qty: int = std\toInt(doc["qty"]);
    std\print("qty={qty} port={port} level={Level.high.value}");

    foreach (path in fs\glob("*.buzz")) {
        std\print(fs\basename(path.value));
    }
}
```

Do not add a `main();` line: magus already calls `main`, so the script would run
twice.{{if .Full}} A script with no `main` runs its top-level statements instead, but
top-level code may only declare things and call functions; `if`, `for` and `while`
belong inside a function.{{end}}

## Coming from TypeScript, Go, Python or Swift

Each row is a mistake observed in practice, with what Buzz wants and what you will
see if you make it. "nothing" means the checker accepts it and the result is wrong.

| you write | Buzz | you see |
| --- | --- | --- |
| `let x = 1;` `const y = 2;` | `var x = 1;` `final y = 2;` | `undefined: let` |
| `function f(n: number): number` | `fun f(n: int) > int` | `expected '{', got ':'` |
| `f(a, b)` | `f(a, b: b)`: every argument after the first is labeled with its parameter name | `argument 2 must be labeled` |
| `std.print(x)` | `std\print(x)`: a module member takes a BACKSLASH | nothing; it runs here, but it is not Buzz |
| `print(x)`, `len(xs)`, `parseInt(s)` | `std\print(x)`, `xs.len()`, `std\parseInt(s)` | `undefined: parseInt`, but `undefined variable "len"` only at run time |
| `cond ? a : b` | `if (cond) a else b` | `unexpected token` |
| `elif` | `else if` | `undefined: elif` |
| `a && !b`, `a \|\| b` | `a and !b`, `a or b` | `unexpected token` |
| `i++` | `i += 1;` or `i = i + 1;` | `unexpected token` |
| `while x < 3 {` | `while (x < 3) {` | `expected '(', got identifier` |
| `for (x of xs)`, `for (x in xs)` | `foreach (x in xs)`, `foreach (i, x in xs)`, `foreach (k, v in map)` | `expected ';', got 'in'` |
| `struct P { x: int; }`, `class` | `object P { x: int, y: int = 0, fun m() > int { ... } }` | `expected '=', got ':'` |
| `P{ x: 1 }`, `new P(1)` | `P{ x = 1 }` | `expected '=', got ':'` |
| `x => x * 2` | `fun (index: int, x: int) > int => x * 2`: list callbacks receive (index, element) | `expected ')', got '=>'` |
| `throw new Error("bad")` | `throw "bad";` in a function declared `> T !> str` | `undefined: new` |
| `catch (e)`, `catch (str e)` | `catch (e: str)` | `"str" is a reserved word` |
| `"n=${n}"` | `"n={n}"` | nothing: it prints `n=$3` |
| `n.toString()`, `s.contains(t)`, `xs.push(v)` | `"{n}"`, `s.indexOf(t) != null`, `xs.append(v)` | `null is not callable` at run time |
| `s[0:3]`, `s.slice(0, 3)` | `s.sub(0, len: 3)` | `expected ']', got ':'` |
| `var xs: [str] = [];` then `xs.append` | `final xs: mut [str] = mut [];` | `requires a mutable list: declare it with mut` |
| `final m = {"a": 1};` then `m["b"] = 2` | `final m = mut {"a": 1};`, or `final m: mut {str: int} = mut {};` | `cannot mutate immutable map` at run time |
| `final c = Counter{};` then a method that assigns `this.n` | `final c = mut Counter{};` | `cannot mutate immutable object` at run time |
| `return null;` from `> int` | declare `> int?`; unwrap with `?? fallback`, `if (x -> v)`, or `x!` | nothing |
| `x = 2;` after `final x = 1;` | `var x = 1;` | `cannot assign to final "x"` |
| `if` or `for` at the top of the file | put it in `main` | `not allowed at the top level` |
| a call to `fs\...`, `json\parse`, ... in a plain function | declare the function `!> any`, or end the call with `catch fallback` | `[BZZ1006] call may raise but is neither declared with !> nor caught` |
| `import "json";` | `import "encoding/json";` (also yaml, toml, csv, xml, base64, hex, url, ini) | `module not found` |
| `total + doc["qty"]` with doc from JSON | `std\toInt(doc["qty"])`: JSON numbers are `double` | `expected int, got double` at run time |
| `json\parse(text).items` | `doc["items"]`, or a typed binding first: `final rows: [any] = json\parse(text);` | `` `any` is not field accessible `` |
| `p.endsWith(".txt")` on a `fs\glob` result | `p.value.endsWith(".txt")`: glob yields `Path`, not `str` | `object Path has no field or method` |
| a name like `out`, `type`, `from`, `match`, `obj`, `str` | another name | `is a reserved word and cannot be used as a name` |

{{if .Full}}Reserved words that cannot be any binding, parameter or field name: `out`,
`from`, `match`, `pat`, `fib`, `rg`, `obj`, `ud`, `zdef`, `typeof`, `type`,
`protocol`, `static`, `extern`, `double`, `any`, `Function`, `int`, `str`, `bool`,
`void`. `test` is NOT reserved: every magus target set defines `export fun
test(...)`. A local named after a module or a builtin (`map`, `len`, `fs`) is not
an error; it SHADOWS the name, and a later call through it dies with `null is not
callable`.

Strings are indexed by BYTE: `s.sub(0, len: 8)` can cut a multi-byte character in
half, and `s.len()` counts bytes. Integer division truncates (`7 / 2` is `3`), and
`//` starts a comment, never an operator.

A raw string is backticks. Use it for JSON, regexes and Mustache templates, since
its quotes need no escaping:

```buzz
template\render(`Hello {{"{{name}}"}}!`, data: {"name": "world"});
```

{{end}}## Built-in methods

These are the methods values have; anything else is `null is not callable` at run
time.

| on | methods |
| --- | --- |
| `str` | `len`, `upper`, `lower`, `trim`, `startsWith`, `endsWith`, `indexOf` (returns `int?`), `split`, `sub(start, len: n)`, `replace(old, with: new)` (every occurrence), `repeat`, `byte`, `utf8Len`, `encodeBase64`, `decodeBase64`, `hex`, `bin` |
| list | `len`, `append`, `insert(i, value: v)`, `remove`, `pop`, `indexOf`, `join`, `sub`, `forEach`, `map`, `filter`, `reduce(fn, initial: v)`, `sort(fn)` (in place, needs `mut`), `reverse`, `fill`, `clone`, `cloneMutable` |
| map | `len`, `size`, `keys`, `values`, `hasKey`, `remove`, `forEach`, `map`, `filter`, `reduce`, `sort`, `diff`, `intersect`, `clone`, `cloneMutable` |
| `std\` | `print`, `assert(cond, message: m)`, `parseInt`, `parseDouble` (both return null on bad input), `toInt`, `toDouble`, `char`, `random`, `panic` |

List callbacks take the index first: `map` and `filter` get `(index, element)`,
`reduce` gets `(index, element, accumulator)`. `sort` gets `(a, b)` and returns
`a < b`. `keys()` returns an immutable list; `.cloneMutable()` it before sorting.

## Host modules: ask, never guess

Every module is imported by its bare name (`import "fs";`), except the data formats,
which sit under `encoding/` (`import "encoding/json";`). Without the import line the
name is `undefined`. For what a module offers:

```sh
{{cmd "describe"}} modules -o name    # every module a script can import
{{cmd "describe"}} module fs          # each method's doc and Signature
```

The parameter names in a Signature ARE the labels: `fs\writeFile(path, content)` is
called `fs\writeFile(target, content: text)`. Brackets mark an optional parameter,
and a variadic one (`fs\join(parts...)`) accepts any label: `fs\join(dir, name: file)`.
A name the module lacks fails the check with `[BZZ1007] module fs has no member`.

WRONG: `strings\toLower(s)`, `json\encode(v)`, `path\join(a, b)`.
CORRECT: `s.lower()`, `json\stringify(v)`, `fs\join(a, name: b)`.

{{if .Full}}Escalate deliberately:

| question | where |
| --- | --- |
| what a module offers, what a method takes and RETURNS | `{{cmd "describe"}} module <name>`, generated from the bindings |
| how a feature works: error sets, fibers, generics, the sandbox | the {{skill "docs-lookup"}} skill |
| what THIS workspace declares (targets, spells, projects) | the {{skill "query"}} skill |

{{end}}### Calling magus from a script

`magus` is a host module too, so it needs `import "magus";`. Ask the workspace through
it rather than running the binary as a subprocess{{if .Full}}: the call is
in-process, version-pinned, and has no argument quoting to get wrong{{end}}.

```buzz
import "std";
import "magus";

fun main(args: [str]) > void !> any {
    foreach (f in magus\describe.file(["MAGUS.md"]).files) {
        std\print("{f.path}: {f.role}");
    }
}
```

`magus\describe.<noun>` returns the typed record `magus describe <noun> -o json` prints.
`magus\cmd(sub, args: [...])` runs any other subcommand. Members that declare into a
loaded workspace (`magus\project`, the provider selections) exist only in a magusfile
and raise {{mgslink "MGS1022"}} anywhere else.

## Test what you write

```buzz
import "std";

fun slugify(text: str) > str {
    return text.trim().lower().replace(" ", with: "-");
}

test "slugify hyphenates" {
    std\assert(slugify("Hello World") == "hello-world", message: "two words");
}
```

`{{cmd "buzz"}} -t file.buzz` prints `ok` or `fail` per block and a summary line. Do
not test `magusfile.buzz` itself{{if .Full}}: it is configuration, so a test of it
tests your configuration, not your logic{{end}}. Move logic worth testing into a
spell or a sibling module and test that. A module a magusfile imports is tested with
`{{cmd "buzz"}} -t --embedded render.buzz`{{if .Full}}, because a magusfile's imports
parse embedded rather than strict, and testing under the strict default would judge
the module by a mode it never runs in{{else}}, since a magusfile's imports parse
embedded, not strict{{end}}.

## Where Buzz code belongs

- **A one-off**: a standalone `.buzz` file run with `{{cmd "buzz"}}`.{{if .Full}} Before
  writing one from nothing, look for a script the workspace keeps to copy; magus's own
  repository indexes its tested scripts in `hack/README.md`.{{end}}
- **Work the workspace repeats**: a target in `magusfile.buzz`{{if .Full}}, so it is
  cached, sandboxed and affected-tracked; a script re-runs from scratch every time{{end}}.
  Targets take `(ctx: magus\Context, args: [str])`.
- **A tool adapter**: a spell, so every project of that type gets its ops.

Reviewing Buzz rather than writing it: {{skill "buzz-review"}}.
