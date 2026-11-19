---
title: term module
generated_from: reference/buzz/
aliases: [modules/term]
description: "Terminal interaction: capability probes, prompts (pick, pickMany, confirm), a spinner, and styled output."
tags: [term, module, stdlib, magusfile]
---

# term

Terminal interaction: capability probes, prompts (pick, pickMany, confirm), a spinner, and styled output. Renders to stderr; every prompt and the spinner raise rather than hanging when there is no terminal.

> **Naming convention:** import the module under its bare name (`import "term"`), reach members with a backslash, and call methods in `camelCase`: `term\someMethod`.

<!-- -->

> [!NOTE]
> The examples below are reference-only. `term` performs real IO (filesystem, process, network, or environment access) that the in-browser playground's sandbox cannot provide, so it is not registered there and its examples have no Run button. Pure-compute modules such as `strings` and `json` run their examples live in the page.

## Methods

### isInteractive

Report whether this run can prompt at all: both standard input and standard error are terminals. Branch on it before calling pick - in CI, behind a pipe, or under a server this is false, and pick would raise. It is the one call that makes an interactive step safe to add to a target that also runs unattended.

**Signature:** `term\isInteractive() -> bool` - [source](https://github.com/egladman/magus/blob/main/std/term.go)

**Returns:** bool

### wantsColor

Report whether styled output should be emitted: standard error is a terminal and the environment does not ask for plain text (NO_COLOR, TERM=dumb). colorize already consults this, so a caller needs it only to make a wider rendering choice - a box-drawing table versus a plain one.

**Signature:** `term\wantsColor() -> bool` - [source](https://github.com/egladman/magus/blob/main/std/term.go)

**Returns:** bool

### size

Return the terminal's {width, height} in character cells. Both are 0 when there is no terminal to measure - piped output, no controlling terminal - so check width rather than expecting a raise. Use it to wrap or truncate output to the reader's actual window instead of assuming 80 columns.

**Signature:** `term\size() -> TermSize` - [source](https://github.com/egladman/magus/blob/main/std/term.go)

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

**Signature:** `term\colorize(s, style) -> string` - [source](https://github.com/egladman/magus/blob/main/std/term.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `s`       | `string` |          |             |
| `style`   | `string` |          |             |

**Returns:** string

### pick

Prompt the reader to choose one of items and return its index. Type to filter (matching every whitespace-separated token), arrow keys or Ctrl-N/Ctrl-P to move, Enter to choose. RAISES when there is no terminal to prompt on - guard with is_interactive - and raises when the reader aborts with ESC, Ctrl-C or Ctrl-D, so a cancel ends the run rather than quietly returning a choice nobody made. Renders to stderr. query, when given, replaces the filter with a live lookup: it is called with the filter text on first paint and after every keystroke and returns the strings to show, best first, so a picker can search something too large to list up front. items may then be empty, the index returned is into the list query returned last, and a query that raises ends the prompt with its error.

**Signature:** `term\pick(items, [prompt], [initialFilter], [initial], [maxRows], [query]) -> int` - [source](https://github.com/egladman/magus/blob/main/std/term.go)

| Parameter       | Type                                                  | Optional | Description |
| --------------- | ----------------------------------------------------- | -------- | ----------- |
| `items`         | `[]string`                                            |          |             |
| `prompt`        | `string`                                              | yes      |             |
| `initialFilter` | `string`                                              | yes      |             |
| `initial`       | `int`                                                 | yes      |             |
| `maxRows`       | `int`                                                 | yes      |             |
| `query`         | `func(context.Context, string) (interface {}, error)` | yes      |             |

**Returns:** int

**Example:**

```buzz
import "std";
import "term";

// A prompt is the interactive spelling of a flag. The flag wins when it was
// passed, and a person on a terminal who left it off is asked. With nothing to
// ask on, or when the reader cancels with ESC or Ctrl-C (pick raises), nothing
// is chosen: the run says which flag it needed.
final projects = ["console", "docs", "libs/gopherbuzz"];
final given: str? = null;

if (given -> project) {
    std\print(project);
} else if (!term\isInteractive()) {
    std\print("--project is required (no terminal to ask on)");
} else {
    try {
        final picked = projects[term\pick(projects, prompt: "project")];
        std\print(term\colorize(picked, style: term\TermStyle.brightGreen));
    } catch (e) {
        std\print("no project chosen: pass --project");
    }
}
```

### pickMany

Prompt the reader to mark any number of items and return the marked ones, in the order items lists them. Type to filter as pick does; Tab marks or unmarks the highlighted row, and a mark survives a change of filter. Enter confirms, and does nothing while nothing is marked, so the highlighted row is never taken for a choice. marked lists the items marked on first paint. Items must be distinct, since the answer is the items themselves, and an entry of marked that is not one of items raises. RAISES when there is no terminal to prompt on - guard with is_interactive - and when the reader aborts with ESC, Ctrl-C or Ctrl-D. Renders to stderr.

**Signature:** `term\pickMany(items, [prompt], [initialFilter], [marked], [maxRows]) -> []string` - [source](https://github.com/egladman/magus/blob/main/std/term.go)

| Parameter       | Type       | Optional | Description |
| --------------- | ---------- | -------- | ----------- |
| `items`         | `[]string` |          |             |
| `prompt`        | `string`   | yes      |             |
| `initialFilter` | `string`   | yes      |             |
| `marked`        | `[]string` | yes      |             |
| `maxRows`       | `int`      | yes      |             |

**Returns:** []string

### confirm

Ask the reader a yes or no question and return true only for yes. Ask once per run, just before a step that cannot be taken back or that reaches outside the machine (a delete, a push, an opened pull request), and print what the step will do before asking: a confirm between read-only steps trains the reader to answer without reading. no is highlighted first, so Enter alone declines. RAISES when there is no terminal to ask on - guard with is_interactive, and let the flag that already says yes (--apply, --push) answer for an unattended run - and raises when the reader aborts with ESC, Ctrl-C or Ctrl-D. Renders to stderr.

**Signature:** `term\confirm(question) -> bool` - [source](https://github.com/egladman/magus/blob/main/std/term.go)

| Parameter  | Type     | Optional | Description |
| ---------- | -------- | -------- | ----------- |
| `question` | `string` |          |             |

**Returns:** bool

**Example:**

```buzz
import "std";
import "term";

// Ask once, right before the step that cannot be taken back, after printing
// what it will do. The flag that already says yes answers for an unattended
// run, and a cancel (confirm raises) is a no.
final apply = false;
std\print("would remove 3 worktrees, freeing 2.1 GiB");

var proceed = apply;
if (!apply and term\isInteractive()) {
    proceed = term\confirm("remove 3 worktrees?") catch false;
}
if (!proceed) {
    std\print("dry run: pass --apply to remove them");
}
```

### spin

Call run and return what it returns, while a loader turns beside label on standard error, so a person watching a long blocking step (a subprocess, a survey of the disk) can tell it from a hang. The loader is erased when run returns or raises, and run's raise is raised again unchanged. Anything run writes to standard error lands under the loader's line, so give it quiet work, such as a proc call whose output is captured. RAISES without calling run when there is no terminal to draw on - guard with is_interactive and call run directly for an unattended run.

**Signature:** `term\spin(label, run) -> any` - [source](https://github.com/egladman/magus/blob/main/std/term.go)

| Parameter | Type                                          | Optional | Description |
| --------- | --------------------------------------------- | -------- | ----------- |
| `label`   | `string`                                      |          |             |
| `run`     | `func(context.Context) (interface {}, error)` |          |             |

**Returns:** any

**Example:**

```buzz
import "proc";
import "std";
import "term";

// A loader turns while the subprocess blocks and is erased when it returns.
// proc captures the command's output, so nothing writes under the loader.
final survey = fun () > any !> any => proc\exec("du", args: ["-sk", "."]).stdout;

try {
    final usage = if (term\isInteractive()) term\spin("surveying the disk", run: survey) else survey();
    std\print("{usage}");
} catch (e) {
    std\print("du failed");
}
```

### notify

Raise a notification into the band magus pins at the bottom of the terminal, where it shows for a few seconds and then disappears on its own. Unlike log.info it does not join the scrolling transcript: it is for something worth GLANCING at during a long run, not for the record. Returns immediately - the message expires on its own clock - and never raises: it is DROPPED when there is no terminal to show it on, or when the band has no room, so a piped or CI run is never given a repainted view it cannot use and no caller has to guard a notification. Log the same fact if it also needs recording. ttlMs defaults to 5000; a negative ttlMs pins the notification until newer ones push it out.

**Signature:** `term\notify(message, [level], [ttlMs])` - [source](https://github.com/egladman/magus/blob/main/std/term.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `message` | `string` |          |             |
| `level`   | `string` | yes      |             |
| `ttlMs`   | `int`    | yes      |             |

**Example:**

```buzz
import "term";

// A notification is something to GLANCE at during a long run, not a record of
// it. It is dropped when there is no terminal to show it on, so anything that
// must survive the run goes through log as well.
term\notify("docs regenerated");

// Severity picks the color; the default ttl is 5 seconds.
term\notify("cache stampede on go-build", level: term\LogLevel.warn);

// A longer ttl for something worth reading twice.
term\notify("deploy skipped: no credentials", level: term\LogLevel.error, ttlMs: 15000);

// A negative ttl keeps it until newer notifications push it out of the band.
term\notify("server unreachable", level: term\LogLevel.error, ttlMs: -1);
```

### clearScreen

Erase the screen and move the cursor home, the repaint a full-screen refresh loop issues before redrawing. Scrollback is preserved, so a reader who scrolls up after the loop ends still sees what came before. A no-op when there is no terminal, so a watch loop needs no guard.

**Signature:** `term\clearScreen()` - [source](https://github.com/egladman/magus/blob/main/std/term.go)

