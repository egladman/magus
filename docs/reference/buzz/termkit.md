---
title: termkit module
generated_from: reference/buzz/
aliases: [modules/termkit]
description: Bindings for the Go package github.com/egladman/magus/libs/termkit, generated from its exported API.
tags: [termkit, module, stdlib, magusfile]
---

# termkit

Bindings for the Go package github.com/egladman/magus/libs/termkit, generated from its exported API. A value with state is an int handle and a record is a map. Writers are standard error and readers standard input.

> **Naming convention:** import the module under its bare name (`import "termkit"`), reach members with a backslash, and call methods in `camelCase`: `termkit\someMethod`.

## Methods

### colorize

Colorize wraps s in an SGR sequence and closes it again, so no caller composes escapes by hand or forgets the reset.

**Signature:** `termkit\colorize(s, sgr) -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `s`       | `string` |          |             |
| `sgr`     | `string` |          |             |

**Returns:** string

### hyperlink

Hyperlink makes text a real clickable link to uri, for terminals that support OSC 8 (iTerm2, kitty, VTE, WezTerm, Windows Terminal), and returns text unchanged for everything else.

**Signature:** `termkit\hyperlink(text, uri) -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `text`    | `string` |          |             |
| `uri`     | `string` |          |             |

**Returns:** string

### clearScreen

ClearScreen erases the screen and homes the cursor, the repaint a full-screen refresh loop issues before redrawing (`magus server status --watch`).

**Signature:** `termkit\clearScreen()` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

### eraseLines

EraseLines erases n lines ending at the cursor and leaves the cursor at column 0 of the topmost erased line, the redraw step for an in-place list that grows and shrinks (the picker).

**Signature:** `termkit\eraseLines(n)` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `n`       | `int` |          |             |

### copy

Copy puts text on the SYSTEM clipboard using OSC 52, reporting whether it was attempted and how much was sent.

**Signature:** `termkit\copy(text) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `text`    | `string` |          |             |

**Returns:** int

### newInlineView

NewInlineView returns a view that redraws its block in place on w, measured through p.

**Signature:** `termkit\newInlineView(p) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `p`       | `int` |          |             |

**Returns:** int

### inlineViewReset

Reset forgets what is on screen, for a caller that cleared the terminal underneath this view and is starting again; the watch loop does this when a frame is too tall to redraw in place and it falls back to erasing the screen.

**Signature:** `termkit\inlineViewReset(inlineView)` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter    | Type  | Optional | Description |
| ------------ | ----- | -------- | ----------- |
| `inlineView` | `int` |          |             |

### inlineViewLines

Lines reports how many rows the block currently occupies.

**Signature:** `termkit\inlineViewLines(inlineView) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter    | Type  | Optional | Description |
| ------------ | ----- | -------- | ----------- |
| `inlineView` | `int` |          |             |

**Returns:** int

### inlineViewClear

Clear erases the block, leaving the terminal as though the view never drew.

**Signature:** `termkit\inlineViewClear(inlineView)` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter    | Type  | Optional | Description |
| ------------ | ----- | -------- | ----------- |
| `inlineView` | `int` |          |             |

### inlineViewPaint

Paint draws frame in place of the previous one, reporting whether it could.

**Signature:** `termkit\inlineViewPaint(inlineView, frame) -> bool` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter    | Type     | Optional | Description |
| ------------ | -------- | -------- | ----------- |
| `inlineView` | `int`    |          |             |
| `frame`      | `string` |          |             |

**Returns:** bool

### inlineViewFinish

Finish moves off the block so a shell prompt does not land on its last line.

**Signature:** `termkit\inlineViewFinish(inlineView)` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter    | Type  | Optional | Description |
| ------------ | ----- | -------- | ----------- |
| `inlineView` | `int` |          |             |

### resetMouseTracking

ResetMouseTracking turns mouse reporting off unconditionally.

**Signature:** `termkit\resetMouseTracking(p)` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `p`       | `int` |          |             |

### openInput

OpenInput puts in into raw mode, turns on mouse reporting on out, and returns the session that reads from it.

**Signature:** `termkit\openInput(p) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `p`       | `int` |          |             |

**Returns:** int

### inputClose

Close turns mouse reporting off and restores the terminal.

**Signature:** `termkit\inputClose(input)` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `input`   | `int` |          |             |

### inputRead

Read blocks until the user does something, and reports it.

**Signature:** `termkit\inputRead(input) -> map[string]any` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `input`   | `int` |          |             |

**Returns:** map[string]any

### inputCursorPosition

CursorPosition asks the terminal where the cursor is, in absolute 1-based terminal coordinates.

**Signature:** `termkit\inputCursorPosition(input) -> any` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `input`   | `int` |          |             |

**Returns:** any

### fixedProbe

FixedProbe answers as a terminal of the given size, whatever the descriptor.

**Signature:** `termkit\fixedProbe(width, height) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `width`   | `int` |          |             |
| `height`  | `int` |          |             |

**Returns:** int

### captureProbe

CaptureProbe models a color-capable terminal for committed recordings.

**Signature:** `termkit\captureProbe(width, height) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `width`   | `int` |          |             |
| `height`  | `int` |          |             |

**Returns:** int

### fd

Fd returns the file descriptor backing w, and whether w has one at all.

**Signature:** `termkit\fd() -> any` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** any

### isTerminalReader

IsTerminalReader reports whether r is a terminal the user is typing at, rather than a pipe, a file, or /dev/null.

**Signature:** `termkit\isTerminalReader(p) -> bool` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `p`       | `int` |          |             |

**Returns:** bool

### stdinIsTerminal

StdinIsTerminal is [IsTerminalReader] for the process's own standard input.

**Signature:** `termkit\stdinIsTerminal() -> bool` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** bool

### isTerminalWriter

IsTerminalWriter reports whether w is a terminal according to p.

**Signature:** `termkit\isTerminalWriter(p) -> bool` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `p`       | `int` |          |             |

**Returns:** bool

### canRender

CanRender reports whether escape sequences may be written to w at all: it must be a terminal, and TERM must not declare one that understands none.

**Signature:** `termkit\canRender(p) -> bool` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `p`       | `int` |          |             |

**Returns:** bool

### wantsHyperlinks

WantsHyperlinks reports whether OSC 8 hyperlinks may be written to w.

**Signature:** `termkit\wantsHyperlinks(p) -> bool` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `p`       | `int` |          |             |

**Returns:** bool

### utf8Locale

UTF8Locale reports whether the environment claims a UTF-8 character set, checked in the order POSIX resolves them: LC_ALL, LC_CTYPE, then LANG.

**Signature:** `termkit\utf8Locale() -> bool` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** bool

### wantsUnicode

WantsUnicode reports whether glyphs beyond ASCII may be drawn: the locale is UTF-8, or p is a CaptureProbe, whose recordings must not depend on the shell.

**Signature:** `termkit\wantsUnicode(p) -> bool` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `p`       | `int` |          |             |

**Returns:** bool

### wantsColor

WantsColor reports whether output written to w should carry ANSI color: w must be a terminal, and NO_COLOR must be unset unless a CaptureProbe fixes the capability for a generated recording.

**Signature:** `termkit\wantsColor(p) -> bool` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `p`       | `int` |          |             |

**Returns:** bool

### prose

Prose writes sentences to w as one paragraph folded to w's width.

**Signature:** `termkit\prose(p, sentences)` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter   | Type       | Optional | Description |
| ----------- | ---------- | -------- | ----------- |
| `p`         | `int`      |          |             |
| `sentences` | `[]string` |          |             |

### proseItem

ProseItem writes label and its sentences as one paragraph whose continuation lines hang under the label, the shape a flag or subcommand list wants.

**Signature:** `termkit\proseItem(p, label, sentences)` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter   | Type       | Optional | Description |
| ----------- | ---------- | -------- | ----------- |
| `p`         | `int`      |          |             |
| `label`     | `string`   |          |             |
| `sentences` | `[]string` |          |             |

### makeRaw

MakeRaw switches the terminal behind fd into raw mode, so a reader receives each keystroke as it is typed rather than a line at a time.

**Signature:** `termkit\makeRaw(fd) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `fd`      | `int` |          |             |

**Returns:** int

### rawModeRestore

Restore puts the terminal back the way MakeRaw found it.

**Signature:** `termkit\rawModeRestore(rawMode)` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `rawMode` | `int` |          |             |

### lineSpansCopy

SpansCopy returns this line's spans as a fresh slice, normalizing the Text+Style shorthand on the way.

**Signature:** `termkit\lineSpansCopy(line) -> any` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `line`    | `map[string]any` |          |             |

**Returns:** any

### resetScrollMargins

ResetScrollMargins clears any DECSTBM margins on w unconditionally.

**Signature:** `termkit\resetScrollMargins(p)` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `p`       | `int` |          |             |

### newZone

NewZone returns a Zone over w, measured through p.

**Signature:** `termkit\newZone(p) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `p`       | `int` |          |             |

**Returns:** int

### stderrZone

StderrZone returns the process-wide owner of standard error's bottom rows.

**Signature:** `termkit\stderrZone() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** int

### releaseStderrZone

ReleaseStderrZone closes the process-wide zone and forgets it, handing back its rows and scroll margins.

**Signature:** `termkit\releaseStderrZone()` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

### zoneWantsColor

WantsColor reports whether this zone's writer should get styled output.

**Signature:** `termkit\zoneWantsColor(zone) -> bool` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `zone`    | `int` |          |             |

**Returns:** bool

### resolveZone

ResolveZone returns the Zone that owns w's bottom rows: the process-wide [StderrZone] when w IS standard error, and a fresh unshared Zone otherwise.

**Signature:** `termkit\resolveZone() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** int

### zoneAcquire

Acquire grants rows at the bottom of the zone, or returns a disabled Lease when it cannot.

**Signature:** `termkit\zoneAcquire(zone, rows) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `zone`    | `int` |          |             |
| `rows`    | `int` |          |             |

**Returns:** int

### leaseEnabled

Enabled reports whether this lease has rows on a real terminal.

**Signature:** `termkit\leaseEnabled(lease) -> bool` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `lease`   | `int` |          |             |

**Returns:** bool

### leaseSet

Set replaces this lease's band and repaints the zone, reporting whether the rows actually reached the terminal.

**Signature:** `termkit\leaseSet(lease, rows) -> bool` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `lease`   | `int` |          |             |
| `rows`    | `any` |          |             |

**Returns:** bool

### leaseRelease

Release gives this lease's rows back and repaints without them.

**Signature:** `termkit\leaseRelease(lease)` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `lease`   | `int` |          |             |

### zoneHitTest

HitTest maps an absolute terminal row (the coordinate space a mouse event arrives in) to the lease that owns it and the index of that row within the lease's band.

**Signature:** `termkit\zoneHitTest(zone, row) -> any` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `zone`    | `int` |          |             |
| `row`     | `int` |          |             |

**Returns:** any

### leaseWidth

Width reports the columns a row of this lease can use, or 0 when it cannot draw.

**Signature:** `termkit\leaseWidth(lease) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `lease`   | `int` |          |             |

**Returns:** int

### zoneHitSpan

HitSpan reports which keyed span a click at (row, col) landed on.

**Signature:** `termkit\zoneHitSpan(zone, row, col) -> any` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `zone`    | `int` |          |             |
| `row`     | `int` |          |             |
| `col`     | `int` |          |             |

**Returns:** any

### leaseGrow

Grow enlarges this lease to rows.

**Signature:** `termkit\leaseGrow(lease, rows)` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `lease`   | `int` |          |             |
| `rows`    | `int` |          |             |

### leaseRows

Rows reports how many rows this lease holds, for a caller bounds-checking an index it got from [Zone.HitTest] against its own content.

**Signature:** `termkit\leaseRows(lease) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `lease`   | `int` |          |             |

**Returns:** int

### zoneClose

Close releases every lease and hands the terminal back.

**Signature:** `termkit\zoneClose(zone)` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `zone`    | `int` |          |             |

### probeIsTerminal

Calls Probe.IsTerminal.

**Signature:** `termkit\probeIsTerminal(probe, fd) -> bool` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `probe`   | `int` |          |             |
| `fd`      | `int` |          |             |

**Returns:** bool

### probeSize

Calls Probe.Size.

**Signature:** `termkit\probeSize(probe, fd) -> any` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `probe`   | `int` |          |             |
| `fd`      | `int` |          |             |

**Returns:** any

### sgrBold

SGR parameter codes.

**Signature:** `termkit\sgrBold() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** string

### sgrDim

SGR parameter codes.

**Signature:** `termkit\sgrDim() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** string

### sgrRed

SGR parameter codes.

**Signature:** `termkit\sgrRed() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** string

### sgrGreen

SGR parameter codes.

**Signature:** `termkit\sgrGreen() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** string

### sgrYellow

SGR parameter codes.

**Signature:** `termkit\sgrYellow() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** string

### sgrBlue

SGR parameter codes.

**Signature:** `termkit\sgrBlue() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** string

### sgrBoldRed

SGR parameter codes.

**Signature:** `termkit\sgrBoldRed() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** string

### sgrReverse

SGRReverse swaps foreground and background.

**Signature:** `termkit\sgrReverse() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** string

### sgrDimGreen

SGR parameter codes.

**Signature:** `termkit\sgrDimGreen() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** string

### sgrDimGrey

SGR parameter codes.

**Signature:** `termkit\sgrDimGrey() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** string

### sgrBrightGreen

SGR parameter codes.

**Signature:** `termkit\sgrBrightGreen() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** string

### selectMark

SelectMark marks the row a keypress or click acts on, in every view, so one gesture always looks like one affordance.

**Signature:** `termkit\selectMark() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** string

### keyUnknown

The keys magus acts on.

**Signature:** `termkit\keyUnknown() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** int

### keyRune

The keys magus acts on.

**Signature:** `termkit\keyRune() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** int

### keyEnter

The keys magus acts on.

**Signature:** `termkit\keyEnter() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** int

### keyEscape

The keys magus acts on.

**Signature:** `termkit\keyEscape() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** int

### keyTab

The keys magus acts on.

**Signature:** `termkit\keyTab() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** int

### keyBackspace

The keys magus acts on.

**Signature:** `termkit\keyBackspace() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** int

### keyUp

The keys magus acts on.

**Signature:** `termkit\keyUp() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** int

### keyDown

The keys magus acts on.

**Signature:** `termkit\keyDown() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** int

### keyLeft

The keys magus acts on.

**Signature:** `termkit\keyLeft() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** int

### keyRight

The keys magus acts on.

**Signature:** `termkit\keyRight() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** int

### keyCtrlC

The keys magus acts on.

**Signature:** `termkit\keyCtrlC() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** int

### keyCtrlD

The keys magus acts on.

**Signature:** `termkit\keyCtrlD() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** int

### keyCtrlN

KeyCtrlN and KeyCtrlP are the emacs-style down/up an incremental selector is expected to answer to, by anyone who has used one.

**Signature:** `termkit\keyCtrlN() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** int

### keyCtrlP

The keys magus acts on.

**Signature:** `termkit\keyCtrlP() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** int

### keyCtrlU

KeyCtrlU clears a line of input.

**Signature:** `termkit\keyCtrlU() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** int

### keyPageUp

KeyPageUp and KeyPageDown page a bounded viewport.

**Signature:** `termkit\keyPageUp() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** int

### keyPageDown

The keys magus acts on.

**Signature:** `termkit\keyPageDown() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** int

### mouseNone

The value of MouseNone.

**Signature:** `termkit\mouseNone() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** int

### mouseLeft

The value of MouseLeft.

**Signature:** `termkit\mouseLeft() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** int

### mouseMiddle

The value of MouseMiddle.

**Signature:** `termkit\mouseMiddle() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** int

### mouseRight

The value of MouseRight.

**Signature:** `termkit\mouseRight() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** int

### mouseWheelUp

The value of MouseWheelUp.

**Signature:** `termkit\mouseWheelUp() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** int

### mouseWheelDown

The value of MouseWheelDown.

**Signature:** `termkit\mouseWheelDown() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** int

### eventKey

The value of EventKey.

**Signature:** `termkit\eventKey() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** int

### eventMouse

The value of EventMouse.

**Signature:** `termkit\eventMouse() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** int

### errNotATerminal

ErrNotATerminal is returned by OpenInput when either end is not a terminal.

**Signature:** `termkit\errNotATerminal() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** string

### systemProbe

SystemProbe is the production Probe: it asks the operating system.

**Signature:** `termkit\systemProbe() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** int

