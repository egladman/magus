---
title: term module
aliases: [modules/term]
description: "Terminal interaction: capability probes, an interactive picker, and styled output."
tags: [term, module, stdlib, magusfile]
---

# term

Terminal interaction: capability probes, an interactive picker, and styled output. Renders to stderr; pick raises rather than hanging when there is no terminal.

> **Naming convention:** import the module under its bare name (`import "term"`), reach members with a backslash, and call methods in `camelCase`: `term\someMethod`.

<!-- -->

> [!NOTE]
> The examples below are reference-only. `term` performs real IO (filesystem, process, network, or environment access) that the in-browser playground's sandbox cannot provide, so it is not registered there and its examples have no Run button. Pure-compute modules such as `strings` and `json` run their examples live in the page.

## Methods

### isInteractive

Report whether this run can prompt at all: both standard input and standard error are terminals. Branch on it before calling pick - in CI, behind a pipe, or under a daemon this is false, and pick would raise. It is the one call that makes an interactive step safe to add to a target that also runs unattended.

**Signature:** `term\isInteractive() → bool` · [source](https://github.com/egladman/magus/blob/main/std/term.go#L103)

**Returns:** bool

### wantsColor

Report whether styled output should be emitted: standard error is a terminal and the environment does not ask for plain text (NO_COLOR, TERM=dumb). colorize already consults this, so a caller needs it only to make a wider rendering choice - a box-drawing table versus a plain one.

**Signature:** `term\wantsColor() → bool` · [source](https://github.com/egladman/magus/blob/main/std/term.go#L108)

**Returns:** bool

### size

Return the terminal's {width, height} in character cells. Both are 0 when there is no terminal to measure - piped output, no controlling terminal - so check width rather than expecting a raise. Use it to wrap or truncate output to the reader's actual window instead of assuming 80 columns.

**Signature:** `term\size() → TermSize` · [source](https://github.com/egladman/magus/blob/main/std/term.go#L113)

**Returns:** map[string]any

**Example:**

```buzz
import "std";
import "term";

// width is 0 when there is no terminal to measure, so check it rather than
// catching an error.
final size = term\size();
final width = if (size.width > 0) size.width else 80;
std\print("wrapping to {width} columns");
```

### colorize

Wrap s in the given style and close it again. Returns s UNCHANGED when the output is not a terminal or the environment asked for plain text, so a magusfile never has to guard the call and escape codes cannot leak into a CI log. A style of none is also pass-through, which lets a conditionally-computed style be passed without branching.

**Signature:** `term\colorize(s, style) → string` · [source](https://github.com/egladman/magus/blob/main/std/term.go#L128)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |
| `style` | `string` |  | |

**Returns:** string

### pick

Prompt the reader to choose one of items and return its index. Type to filter (matching every whitespace-separated token), arrow keys or Ctrl-N/Ctrl-P to move, Enter to choose. RAISES when there is no terminal to prompt on - guard with is_interactive - and raises when the reader aborts with ESC, Ctrl-C or Ctrl-D, so a cancel ends the run rather than quietly returning a choice nobody made. Renders to stderr.

**Signature:** `term\pick(items, [prompt], [initial_filter], [initial], [max_rows]) → int` · [source](https://github.com/egladman/magus/blob/main/std/term.go#L136)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `items` | `[]string` |  | |
| `prompt` | `string` | yes | |
| `initial_filter` | `string` | yes | |
| `initial` | `int` | yes | |
| `max_rows` | `int` | yes | |

**Returns:** int

**Example:**

```buzz
import "std";
import "term";

// pick RAISES when there is no terminal, so an interactive step that also has to
// run unattended guards on isInteractive and declares its own default.
final projects = ["console", "docs", "libs/gopherbuzz"];

final chosen = if (term\isInteractive())
    projects[term\pick(projects, prompt: "project")]
else
    projects[0];

std\print(term\colorize(chosen, term\TermStyle.brightGreen));
```

### clearScreen

Erase the screen and move the cursor home, the repaint a full-screen refresh loop issues before redrawing. Scrollback is preserved, so a reader who scrolls up after the loop ends still sees what came before. A no-op when there is no terminal, so a watch loop needs no guard.

**Signature:** `term\clearScreen()` · [source](https://github.com/egladman/magus/blob/main/std/term.go#L172)

