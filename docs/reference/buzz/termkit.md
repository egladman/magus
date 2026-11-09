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

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `s`       | `<invalid>` |          |             |
| `sgr`     | `<invalid>` |          |             |

**Returns:** <invalid>

### hyperlink

Hyperlink makes text a real clickable link to uri, for terminals that support OSC 8 (iTerm2, kitty, VTE, WezTerm, Windows Terminal), and returns text unchanged for everything else.

**Signature:** `termkit\hyperlink(text, uri) -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `text`    | `<invalid>` |          |             |
| `uri`     | `<invalid>` |          |             |

**Returns:** <invalid>

### clearScreen

ClearScreen erases the screen and homes the cursor, the repaint a full-screen refresh loop issues before redrawing (`magus server status --watch`).

**Signature:** `termkit\clearScreen()` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

### eraseLines

EraseLines erases n lines ending at the cursor and leaves the cursor at column 0 of the topmost erased line, the redraw step for an in-place list that grows and shrinks (the picker).

**Signature:** `termkit\eraseLines(n)` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `n`       | `<invalid>` |          |             |

### copy

Copy puts text on the SYSTEM clipboard using OSC 52, reporting whether it was attempted and how much was sent.

**Signature:** `termkit\copy(text) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `text`    | `<invalid>` |          |             |

**Returns:** <invalid>

### newInlineView

NewInlineView returns a view that redraws its block in place on w, measured through p.

**Signature:** `termkit\newInlineView(p) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `p`       | `<invalid>` |          |             |

**Returns:** <invalid>

### inlineViewReset

Reset forgets what is on screen, for a caller that cleared the terminal underneath this view and is starting again; the watch loop does this when a frame is too tall to redraw in place and it falls back to erasing the screen.

**Signature:** `termkit\inlineViewReset(inlineView)` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter    | Type        | Optional | Description |
| ------------ | ----------- | -------- | ----------- |
| `inlineView` | `<invalid>` |          |             |

### inlineViewLines

Lines reports how many rows the block currently occupies.

**Signature:** `termkit\inlineViewLines(inlineView) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter    | Type        | Optional | Description |
| ------------ | ----------- | -------- | ----------- |
| `inlineView` | `<invalid>` |          |             |

**Returns:** <invalid>

### inlineViewClear

Clear erases the block, leaving the terminal as though the view never drew.

**Signature:** `termkit\inlineViewClear(inlineView)` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter    | Type        | Optional | Description |
| ------------ | ----------- | -------- | ----------- |
| `inlineView` | `<invalid>` |          |             |

### inlineViewPaint

Paint draws frame in place of the previous one, reporting whether it could.

**Signature:** `termkit\inlineViewPaint(inlineView, frame) -> bool` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter    | Type        | Optional | Description |
| ------------ | ----------- | -------- | ----------- |
| `inlineView` | `<invalid>` |          |             |
| `frame`      | `<invalid>` |          |             |

**Returns:** <invalid>

### inlineViewFinish

Finish moves off the block so a shell prompt does not land on its last line.

**Signature:** `termkit\inlineViewFinish(inlineView)` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter    | Type        | Optional | Description |
| ------------ | ----------- | -------- | ----------- |
| `inlineView` | `<invalid>` |          |             |

### resetMouseTracking

ResetMouseTracking turns mouse reporting off unconditionally.

**Signature:** `termkit\resetMouseTracking(p)` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `p`       | `<invalid>` |          |             |

### openInput

OpenInput puts in into raw mode, turns on mouse reporting on out, and returns the session that reads from it.

**Signature:** `termkit\openInput(p) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `p`       | `<invalid>` |          |             |

**Returns:** <invalid>

### inputClose

Close turns mouse reporting off and restores the terminal.

**Signature:** `termkit\inputClose(input)` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `input`   | `<invalid>` |          |             |

### inputRead

Read blocks until the user does something, and reports it.

**Signature:** `termkit\inputRead(input) -> map[string]any` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `input`   | `<invalid>` |          |             |

**Returns:** <invalid>

### inputCursorPosition

CursorPosition asks the terminal where the cursor is, in absolute 1-based terminal coordinates.

**Signature:** `termkit\inputCursorPosition(input) -> any` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `input`   | `<invalid>` |          |             |

**Returns:** <invalid>

### fixedProbe

FixedProbe answers as a terminal of the given size, whatever the descriptor.

**Signature:** `termkit\fixedProbe(width, height) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `width`   | `<invalid>` |          |             |
| `height`  | `<invalid>` |          |             |

**Returns:** <invalid>

### captureProbe

CaptureProbe models a color-capable terminal for committed recordings.

**Signature:** `termkit\captureProbe(width, height) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `width`   | `<invalid>` |          |             |
| `height`  | `<invalid>` |          |             |

**Returns:** <invalid>

### fd

Fd returns the file descriptor backing w, and whether w has one at all.

**Signature:** `termkit\fd() -> any` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### isTerminalReader

IsTerminalReader reports whether r is a terminal the user is typing at, rather than a pipe, a file, or /dev/null.

**Signature:** `termkit\isTerminalReader(p) -> bool` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `p`       | `<invalid>` |          |             |

**Returns:** <invalid>

### stdinIsTerminal

StdinIsTerminal is [IsTerminalReader] for the process's own standard input.

**Signature:** `termkit\stdinIsTerminal() -> bool` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### isTerminalWriter

IsTerminalWriter reports whether w is a terminal according to p.

**Signature:** `termkit\isTerminalWriter(p) -> bool` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `p`       | `<invalid>` |          |             |

**Returns:** <invalid>

### canRender

CanRender reports whether escape sequences may be written to w at all: it must be a terminal, and TERM must not declare one that understands none.

**Signature:** `termkit\canRender(p) -> bool` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `p`       | `<invalid>` |          |             |

**Returns:** <invalid>

### wantsHyperlinks

WantsHyperlinks reports whether OSC 8 hyperlinks may be written to w.

**Signature:** `termkit\wantsHyperlinks(p) -> bool` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `p`       | `<invalid>` |          |             |

**Returns:** <invalid>

### utf8Locale

UTF8Locale reports whether the environment claims a UTF-8 character set, checked in the order POSIX resolves them: LC_ALL, LC_CTYPE, then LANG.

**Signature:** `termkit\utf8Locale() -> bool` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### wantsUnicode

WantsUnicode reports whether glyphs beyond ASCII may be drawn: the locale is UTF-8, or p is a CaptureProbe, whose recordings must not depend on the shell.

**Signature:** `termkit\wantsUnicode(p) -> bool` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `p`       | `<invalid>` |          |             |

**Returns:** <invalid>

### wantsColor

WantsColor reports whether output written to w should carry ANSI color: w must be a terminal, and NO_COLOR must be unset unless a CaptureProbe fixes the capability for a generated recording.

**Signature:** `termkit\wantsColor(p) -> bool` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `p`       | `<invalid>` |          |             |

**Returns:** <invalid>

### prose

Prose writes sentences to w as one paragraph folded to w's width.

**Signature:** `termkit\prose(p, sentences)` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter   | Type        | Optional | Description |
| ----------- | ----------- | -------- | ----------- |
| `p`         | `<invalid>` |          |             |
| `sentences` | `<invalid>` |          |             |

### proseItem

ProseItem writes label and its sentences as one paragraph whose continuation lines hang under the label, the shape a flag or subcommand list wants.

**Signature:** `termkit\proseItem(p, label, sentences)` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter   | Type        | Optional | Description |
| ----------- | ----------- | -------- | ----------- |
| `p`         | `<invalid>` |          |             |
| `label`     | `<invalid>` |          |             |
| `sentences` | `<invalid>` |          |             |

### makeRaw

MakeRaw switches the terminal behind fd into raw mode, so a reader receives each keystroke as it is typed rather than a line at a time.

**Signature:** `termkit\makeRaw(fd) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `fd`      | `<invalid>` |          |             |

**Returns:** <invalid>

### rawModeRestore

Restore puts the terminal back the way MakeRaw found it.

**Signature:** `termkit\rawModeRestore(rawMode)` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `rawMode` | `<invalid>` |          |             |

### lineSpansCopy

SpansCopy returns this line's spans as a fresh slice, normalizing the Text+Style shorthand on the way.

**Signature:** `termkit\lineSpansCopy(line) -> any` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `line`    | `<invalid>` |          |             |

**Returns:** <invalid>

### resetScrollMargins

ResetScrollMargins clears any DECSTBM margins on w unconditionally.

**Signature:** `termkit\resetScrollMargins(p)` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `p`       | `<invalid>` |          |             |

### newZone

NewZone returns a Zone over w, measured through p.

**Signature:** `termkit\newZone(p) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `p`       | `<invalid>` |          |             |

**Returns:** <invalid>

### stderrZone

StderrZone returns the process-wide owner of standard error's bottom rows.

**Signature:** `termkit\stderrZone() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### releaseStderrZone

ReleaseStderrZone closes the process-wide zone and forgets it, handing back its rows and scroll margins.

**Signature:** `termkit\releaseStderrZone()` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

### zoneWantsColor

WantsColor reports whether this zone's writer should get styled output.

**Signature:** `termkit\zoneWantsColor(zone) -> bool` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `zone`    | `<invalid>` |          |             |

**Returns:** <invalid>

### resolveZone

ResolveZone returns the Zone that owns w's bottom rows: the process-wide [StderrZone] when w IS standard error, and a fresh unshared Zone otherwise.

**Signature:** `termkit\resolveZone() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### zoneAcquire

Acquire grants rows at the bottom of the zone, or returns a disabled Lease when it cannot.

**Signature:** `termkit\zoneAcquire(zone, rows) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `zone`    | `<invalid>` |          |             |
| `rows`    | `<invalid>` |          |             |

**Returns:** <invalid>

### leaseEnabled

Enabled reports whether this lease has rows on a real terminal.

**Signature:** `termkit\leaseEnabled(lease) -> bool` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `lease`   | `<invalid>` |          |             |

**Returns:** <invalid>

### leaseSet

Set replaces this lease's band and repaints the zone, reporting whether the rows actually reached the terminal.

**Signature:** `termkit\leaseSet(lease, rows) -> bool` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `lease`   | `<invalid>` |          |             |
| `rows`    | `<invalid>` |          |             |

**Returns:** <invalid>

### leaseRelease

Release gives this lease's rows back and repaints without them.

**Signature:** `termkit\leaseRelease(lease)` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `lease`   | `<invalid>` |          |             |

### zoneHitTest

HitTest maps an absolute terminal row (the coordinate space a mouse event arrives in) to the lease that owns it and the index of that row within the lease's band.

**Signature:** `termkit\zoneHitTest(zone, row) -> any` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `zone`    | `<invalid>` |          |             |
| `row`     | `<invalid>` |          |             |

**Returns:** <invalid>

### leaseWidth

Width reports the columns a row of this lease can use, or 0 when it cannot draw.

**Signature:** `termkit\leaseWidth(lease) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `lease`   | `<invalid>` |          |             |

**Returns:** <invalid>

### zoneHitSpan

HitSpan reports which keyed span a click at (row, col) landed on.

**Signature:** `termkit\zoneHitSpan(zone, row, col) -> any` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `zone`    | `<invalid>` |          |             |
| `row`     | `<invalid>` |          |             |
| `col`     | `<invalid>` |          |             |

**Returns:** <invalid>

### leaseGrow

Grow enlarges this lease to rows.

**Signature:** `termkit\leaseGrow(lease, rows)` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `lease`   | `<invalid>` |          |             |
| `rows`    | `<invalid>` |          |             |

### leaseRows

Rows reports how many rows this lease holds, for a caller bounds-checking an index it got from [Zone.HitTest] against its own content.

**Signature:** `termkit\leaseRows(lease) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `lease`   | `<invalid>` |          |             |

**Returns:** <invalid>

### zoneClose

Close releases every lease and hands the terminal back.

**Signature:** `termkit\zoneClose(zone)` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `zone`    | `<invalid>` |          |             |

### probeIsTerminal

Calls Probe.IsTerminal.

**Signature:** `termkit\probeIsTerminal(probe, fd) -> bool` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `probe`   | `<invalid>` |          |             |
| `fd`      | `<invalid>` |          |             |

**Returns:** <invalid>

### probeSize

Calls Probe.Size.

**Signature:** `termkit\probeSize(probe, fd) -> any` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `probe`   | `<invalid>` |          |             |
| `fd`      | `<invalid>` |          |             |

**Returns:** <invalid>

### sgrBold

SGR parameter codes.

**Signature:** `termkit\sgrBold() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### sgrDim

SGR parameter codes.

**Signature:** `termkit\sgrDim() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### sgrRed

SGR parameter codes.

**Signature:** `termkit\sgrRed() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### sgrGreen

SGR parameter codes.

**Signature:** `termkit\sgrGreen() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### sgrYellow

SGR parameter codes.

**Signature:** `termkit\sgrYellow() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### sgrBlue

SGR parameter codes.

**Signature:** `termkit\sgrBlue() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### sgrBoldRed

SGR parameter codes.

**Signature:** `termkit\sgrBoldRed() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### sgrReverse

SGRReverse swaps foreground and background.

**Signature:** `termkit\sgrReverse() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### sgrDimGreen

SGR parameter codes.

**Signature:** `termkit\sgrDimGreen() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### sgrDimGrey

SGR parameter codes.

**Signature:** `termkit\sgrDimGrey() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### sgrBrightGreen

SGR parameter codes.

**Signature:** `termkit\sgrBrightGreen() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### selectMark

SelectMark marks the row a keypress or click acts on, in every view, so one gesture always looks like one affordance.

**Signature:** `termkit\selectMark() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### keyUnknown

The keys magus acts on.

**Signature:** `termkit\keyUnknown() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### keyRune

The keys magus acts on.

**Signature:** `termkit\keyRune() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### keyEnter

The keys magus acts on.

**Signature:** `termkit\keyEnter() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### keyEscape

The keys magus acts on.

**Signature:** `termkit\keyEscape() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### keyTab

The keys magus acts on.

**Signature:** `termkit\keyTab() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### keyBackspace

The keys magus acts on.

**Signature:** `termkit\keyBackspace() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### keyUp

The keys magus acts on.

**Signature:** `termkit\keyUp() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### keyDown

The keys magus acts on.

**Signature:** `termkit\keyDown() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### keyLeft

The keys magus acts on.

**Signature:** `termkit\keyLeft() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### keyRight

The keys magus acts on.

**Signature:** `termkit\keyRight() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### keyCtrlC

The keys magus acts on.

**Signature:** `termkit\keyCtrlC() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### keyCtrlD

The keys magus acts on.

**Signature:** `termkit\keyCtrlD() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### keyCtrlN

KeyCtrlN and KeyCtrlP are the emacs-style down/up an incremental selector is expected to answer to, by anyone who has used one.

**Signature:** `termkit\keyCtrlN() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### keyCtrlP

The keys magus acts on.

**Signature:** `termkit\keyCtrlP() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### keyCtrlU

KeyCtrlU clears a line of input.

**Signature:** `termkit\keyCtrlU() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### keyPageUp

KeyPageUp and KeyPageDown page a bounded viewport.

**Signature:** `termkit\keyPageUp() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### keyPageDown

The keys magus acts on.

**Signature:** `termkit\keyPageDown() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### mouseNone

The value of MouseNone.

**Signature:** `termkit\mouseNone() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### mouseLeft

The value of MouseLeft.

**Signature:** `termkit\mouseLeft() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### mouseMiddle

The value of MouseMiddle.

**Signature:** `termkit\mouseMiddle() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### mouseRight

The value of MouseRight.

**Signature:** `termkit\mouseRight() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### mouseWheelUp

The value of MouseWheelUp.

**Signature:** `termkit\mouseWheelUp() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### mouseWheelDown

The value of MouseWheelDown.

**Signature:** `termkit\mouseWheelDown() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### eventKey

The value of EventKey.

**Signature:** `termkit\eventKey() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### eventMouse

The value of EventMouse.

**Signature:** `termkit\eventMouse() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### errNotATerminal

ErrNotATerminal is returned by OpenInput when either end is not a terminal.

**Signature:** `termkit\errNotATerminal() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

### systemProbe

SystemProbe is the production Probe: it asks the operating system.

**Signature:** `termkit\systemProbe() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/termkit/module.go)

**Returns:** <invalid>

