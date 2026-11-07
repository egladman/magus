---
title: Terminal output stays copyable
order: 37
description: A live region writes only real text; no trailing fill, no frame runes on a content row, a layout that stacks instead of splitting into columns, and nothing left in scrollback when it ends.
tags: [design, principle, cli, output, terminal]
---

# Terminal output stays copyable

A terminal selects linearly, one row after another, and takes every cell it
finds on the way. A frame is made of cells. So is the padding that carries a
right edge to the same column on every row, and so is a second column beside the
first. Drag across a boxed band and the paste holds the box.

So the pinned band writes only text. A row ends where its words end, with no
spaces after them to reach an edge. No rule or side edge frames the band, and
no divider runs down it. When two things want the same rows, one goes under the
other. Metadata sits after the longest label in its block rather than at the
terminal edge, so the gap between the two is inside the row and never trails it.
When the run ends the band is erased, and what stays in scrollback is the
transcript.

The same holds for the `magus x` picker: a prompt row, the items, a hint row.

Nothing here makes the band the thing to copy from. Press `y` for a failure's
output on the clipboard, or `o` to print it into the transcript. What changes
is that a drag across the band no longer collects punctuation that was never
output.

## Enforced by

- `internal/interactive/screen` records which cells a program wrote, and
  `TrailingSpaceRows` names every row whose last written cell is a space.
- The band tests drive the running, wait, failure and preview states through a
  fake terminal at 80, 120 and 200 columns and fail on any such row, and on a
  release that leaves the zone with text in it.
- `tty` has no frame to draw: a region row is the erase-line sequence plus its
  spans, clipped to the terminal width less one, and nothing pads it.

## Good

Metadata in a column, rows ending in their last word:

```text
  ✗ internal/sandbox
  ▸ ╰ test                       4.1s
  ✗ std
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
