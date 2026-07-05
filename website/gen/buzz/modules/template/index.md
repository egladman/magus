---
title: template module
aliases: [modules/template]
description: Logic-less Mustache templating (Mustache spec, via github.com/cbroglie/mustache).
tags: [template, module, stdlib, magusfile]
---

# template

Logic-less Mustache templating (Mustache spec, via github.com/cbroglie/mustache).

> **Naming convention:** import the module under its bare name (`import "template"`) and call methods in `camelCase` (`template.someMethod`).

## Methods

### render

Render a Mustache template against a context value (usually a name->value map; lists drive sections, absent/false keys hide them). Returns the filled string; errors on a malformed template.

**Signature:** `template.render(template, data) → string` · [source](https://github.com/egladman/magus/blob/main/std/template.go#L42)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `template` | `string` |  | |
| `data` | `any` |  | |

**Returns:** string

**Example:**

<!-- run -->
```buzz
import "std";
import "template";

// Backtick raw strings hold the Mustache verbatim; a double-quoted "{{name}}"
// would collide with Buzz's own {expr} string interpolation.
std.print(template.render(`Hello {{name}}`, {"name": "world"}));
// -> "Hello world"
```

### render_partials

Render a Mustache template that includes partials via {{>name}}, resolving each name against the partials map (name->template string). Partials may reference other partials. Same context and escaping rules as render; errors on a malformed template.

**Signature:** `template.renderPartials(template, data, partials) → string` · [source](https://github.com/egladman/magus/blob/main/std/template.go#L53)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `template` | `string` |  | |
| `data` | `any` |  | |
| `partials` | `map[string]string` |  | |

**Returns:** string

**Example:**

<!-- run -->
```buzz
import "std";
import "template";

// Shared chrome lives in partials; the page template pulls them in with {{>name}}.
// Backtick raw strings hold the Mustache verbatim (a double-quoted string would
// collide with Buzz's own {expr} interpolation).
final page = `{{>header}}<main>{{body}}</main>{{>footer}}`;
final partials = {
    "header": `<header>{{title}}</header>`,
    "footer": `<footer>{{title}}</footer>`,
};

std.print(template.renderPartials(page, {"title": "magus", "body": "hi"}, partials));
// -> "<header>magus</header><main>hi</main><footer>magus</footer>"
```

