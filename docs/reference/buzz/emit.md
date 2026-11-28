---
title: emit module
generated_from: reference/buzz/
aliases: [modules/emit]
description: "Code templates as values with typed holes: load a template file, fill its holes, render it for an output path and plan new files for splice to preview and apply."
tags: [emit, module, stdlib, magusfile]
---

# emit

Code templates as values with typed holes: load a template file, fill its holes, render it for an output path and plan new files for splice to preview and apply.

> **Naming convention:** import the module under its bare name (`import "emit"`), reach members with a backslash, and call methods in `camelCase`: `emit\someMethod`.

<!-- -->

> [!NOTE]
> The examples below are reference-only. `emit` performs real IO (filesystem, process, network, or environment access) that the in-browser playground's sandbox cannot provide, so it is not registered there and its examples have no Run button. Pure-compute modules such as `strings` and `json` run their examples live in the page.

## Methods

### load

Read a template file and bind every hole to its value. values must name every hole and nothing else, and no value may be null. Reads through the fs read policy and writes nothing. The Result's error lists every problem in the file, one per line.

**Signature:** `emit\load(path, values) -> result\Result::<Template>` - [source](https://github.com/egladman/magus/blob/main/std/emit.go)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `path`    | `string`         |          |             |
| `values`  | `map[string]any` |          |             |

**Returns:** map[string]any

### text

A str as static text: a template with no holes. No hole is parsed out of text.

**Signature:** `emit\text(text) -> Template` - [source](https://github.com/egladman/magus/blob/main/std/emit.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `text`    | `string` |          |             |

**Returns:** map[string]any

### hole

A str or scalar as one hole with its encoding: a template of one hole. An int, a double or a bool is written in its string form, as !s writes it. expression names the hole in a refusal.

**Signature:** `emit\hole(value, spec, [expression]) -> Template` - [source](https://github.com/egladman/magus/blob/main/std/emit.go)

| Parameter    | Type     | Optional | Description |
| ------------ | -------- | -------- | ----------- |
| `value`      | `any`    |          |             |
| `spec`       | `string` |          |             |
| `expression` | `string` | yes      |             |

**Returns:** map[string]any

### join

Parts joined with sep between them. sep is static text, and sep "" is template concatenation.

**Signature:** `emit\join(parts, sep) -> Template` - [source](https://github.com/egladman/magus/blob/main/std/emit.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `parts`   | `any`    |          |             |
| `sep`     | `string` |          |             |

**Returns:** map[string]any

### render

The text a template becomes as the file at path. path's extension picks the encodings through the workspace's spell languages; no file is read or written.

**Signature:** `emit\render(template, path) -> result\Result::<str>` - [source](https://github.com/egladman/magus/blob/main/std/emit.go)

| Parameter  | Type             | Optional | Description |
| ---------- | ---------------- | -------- | ----------- |
| `template` | `map[string]any` |          |             |
| `path`     | `string`         |          |             |

**Returns:** map[string]any

**Example:**

```buzz
import "emit";
import "std";

fun main(args: [str]) > void !> any {
    // Holes built in Buzz: each names its encoding, and join writes the separator.
    final names = emit\join([
        emit\hole("Round", spec: "ident", expression: "first"),
        emit\hole("Zero", spec: "ident", expression: "second"),
    ], sep: ", ");
    final line = emit\join([emit\text("var _ = []any\{"), names, emit\text("}\n")], sep: "");

    final text = emit\render(line, path: "price/names.go");
    if (text.error -> err) { throw err.message; }
    std\print(text.value!);
    // -> "var _ = []any{Round, Zero}\n"
}
```

### file

A file to write: path, resolved as fs paths are when planned, and the template it holds.

**Signature:** `emit\file(path, template) -> EmitFile` - [source](https://github.com/egladman/magus/blob/main/std/emit.go)

| Parameter  | Type             | Optional | Description |
| ---------- | ---------------- | -------- | ----------- |
| `path`     | `string`         |          |             |
| `template` | `map[string]any` |          |             |

**Returns:** map[string]any

### plan

Render every file and resolve each as a new file under the lease, writing nothing. A path named twice, an output with no final newline and a path that exists with other bytes are refused. Refusals are fields of the plan; splice\diff previews it and splice\apply writes it.

**Signature:** `emit\plan(files) -> SplicePlan` - [source](https://github.com/egladman/magus/blob/main/std/emit.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `files`   | `any` |          |             |

**Returns:** map[string]any

**Example:**

```buzz
import "emit";
import "result";
import "splice";
import "std";

object Case { name: str, fn: str, arg: str, want: str }

// suiteFile loads one template per case, nests them in the suite template and
// names the file the suite becomes.
fun suiteFile(dir: str, cases: [Case]) > result\Result::<emit\EmitFile> {
    final parts = mut [<emit\Template>];
    foreach (c in cases) {
        final values: {str: any} = {
            "suite": "priceSuite", "test": "Test{c.name}", "pkg": "price",
            "fn": c.fn, "arg": c.arg, "want": c.want,
        };
        final one = emit\load("{dir}/case.go.tmpl", values: values);
        if (one.error -> err) { return result\Result::<emit\EmitFile>{ error = err }; }
        parts.append(one.value!);
    }
    final values: {str: any} = {
        "testPkg": "price_test", "importPath": "example.com/shop/price",
        "suite": "priceSuite", "entry": "TestPriceSuite",
        "cases": emit\join(parts, sep: "\n"),
    };
    final whole = emit\load("{dir}/suite_test.go.tmpl", values: values);
    if (whole.error -> err) { return result\Result::<emit\EmitFile>{ error = err }; }
    return result\Result::<emit\EmitFile>{ value = emit\file("price/price_suite_test.go", template: whole.value!) };
}

fun main(args: [str]) > void !> any {
    final cases = [
        Case{ name = "Round", fn = "Format", arg = "1.499", want = "1.50" },
        Case{ name = "Zero", fn = "Format", arg = "0", want = "0.00" },
    ];
    final file = suiteFile("std/examples/emit", cases: cases);
    if (file.error -> err) { throw err.message; }

    // emit plans new files only; splice previews the plan and writes it.
    final plan = emit\plan([file.value!]);
    final preview = splice\diff(plan);
    if (preview.error -> err) { throw err.message; }
    std\print(preview.value!);

    if (args.len() > 0 and args[0] == "apply") {
        final done = splice\apply(plan);
        if (done.error -> err) { throw err.message; }
    }
}
```

