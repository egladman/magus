---
title: Terminal output stays copyable
order: 37
description: Scrollback is one column of records; the live band reads left to right as one record per row; a tiled view lives only behind a flag on the alternate screen.
tags: [design, principle, cli, output, terminal]
---

# Terminal output stays copyable

A terminal selects linearly, one row after another, and takes every cell it
finds on the way. A frame is made of cells. So is the padding that carries a
right edge to the same column on every row, and so is a second column beside the
first. Drag across a boxed band and the paste holds the box.

So magus draws in three tiers, and each tier says what it may do.

1. **Scrollback is one column.** A row is a record and ends at its last word.
   Nothing is tiled, padded to the terminal edge, or framed. What a reader drags
   across is what was output.
2. **The live band may use table rows, read left to right as one record.** A
   row holds a status, a label and its metadata, aligned to the longest label in
   its block. The band never holds independent side-by-side panes: when two
   things want the same rows, one goes under the other. The band is erased when
   the run ends, so scrollback keeps only the transcript.
3. **A tiled view lives only behind an explicit command or flag**, on the
   alternate screen, with `y` and `o` keys that copy or print what the reader
   wants, and a plain summary in scrollback on exit. Tiling across views is the
   terminal multiplexer's job, not magus's.

A status is a word in a four-column slot: `ok`, `FAIL`, `warn`, `skip`. A word
copies as a word and reads without color. The spinner, the tree branches and
the `▸` selection mark stay, since they are structure rather than status.

The same holds for the `magus x` picker: a prompt row, the items, a hint row.

Nothing here makes the band the thing to copy from. Press `y` for a failure's
output on the clipboard, or `o` to print it into the transcript. What changes
is that a drag across the band no longer collects punctuation that was never
output.

## Enforced by

- `libs/termkit/screen` records which cells a program wrote, and
  `TrailingSpaceRows` names every row whose last written cell is a space.
- The band tests drive the running, wait, failure and preview states through
  that fake terminal at 80, 120 and 200 columns and fail on any such row, and
  on a release that leaves the zone with text in it.
- Every frame of every `libs/termkit/progress` loader is tested free of
  trailing spaces, except the span loaders, which fill their width by design.
- `libs/termkit` has no frame to draw: a band row is the erase-line sequence
  plus its spans, clipped to the terminal width less one, and nothing pads it.

## Good

Status words in a slot, metadata in a column, rows ending in their last word:

```text
FAIL  3 failed · 0 ok · 6.4s
  FAIL  internal/sandbox
  ▸ ╰ test                       4.1s
  FAIL  std
    ├ lint                       2.3s
    ╰ test                       1.1s
```

## Bad

```text
╭─ 3 failed ───────────────────────────────────────╮
│ ✗ internal/sandbox                     │ === RUN │
│   ╰ test                       4.1s    │ FAIL    │
╰──────────────────────────────────────────────────╯
```

Selecting the left column takes the right one with it, the padding rides along
on every row, and the rules and edges arrive as characters nobody typed.

## See also

- [No news is good news](no-news-is-good-news.md)
- [Human-first is the AI integration](human-first-is-the-ai-integration.md)
- [Terminal](../../concepts/terminal.md)
- [termkit](../../reference/termkit.md)
