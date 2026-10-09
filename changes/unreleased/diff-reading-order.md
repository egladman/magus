### Added

- **`magus diff` prints the order to read the hunks in.** Hunks are grouped by definition
  and use among the changed symbols: a definition before its uses, an interface before its
  implementations, a test after the code it exercises, larger and wider-reaching groups
  first. Each hunk carries the sentence naming the relationship that placed it, and a
  completeness line (`14 hunks, 14 placed`) names any hunk missing or repeated and any
  changed file with no hunk to show. Generated output sits in a folded group, and a hunk
  magus cannot place goes in a final `unranked` group that says why. The same diff and index
  give the same order, and no model chooses it. The text report, the terminal viewer, `-o json`
  (as `order`) and the console's focus mode follow it. When the symbol index cannot be
  brought current, or a touched project that can be indexed has none built, the order is
  left out and a note names `magus graph build`.
- **`magus diff --unread` lists the hunks no read mark covers.** Marks are keyed by hunk
  content, so an edited hunk is unread again. It takes the same sources as the
  rest of `magus diff` (the working tree, `--rev`, a patch), prints text or `-o json`, and
  always exits 0. Where the marks cannot be read it says the state is
  unknown and calls no hunk unread.
- **`magus diff --print-hook` prints a pre-push hook.** The script runs `--unread` on each
  range being pushed, writes to stderr and exits 0, so it never holds a push up. magus prints
  it and never installs it.
