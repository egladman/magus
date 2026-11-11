### Added

- **The Claude Code mod's status line shows what the session and the day have cost.**
  The session figure is Claude Code's own `/cost` total; the day's is summed across
  sessions in the mod's store. Where Claude Code reports no total, the mod's sum at
  magus's prices stands in, marked `(magus prices)`. The budget gate counts the same
  total.

### Changed

- **The Claude Code mod prices from magus's model rows, and its `ratesFile` setting
  is gone.** It reads the `models` file `magus agent harness install` writes to
  `.claude/magus/tools/`, the table the guard prices with, instead of a copy of its
  own. Without that file nothing is priced, and the band names the install. To
  change a price, change the harness spell's `mgs_listModels` rows.
