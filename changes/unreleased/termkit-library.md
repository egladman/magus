### Added

- **termkit, the terminal UI library magus draws with, is public.** `libs/termkit` is
  its own Go module with packages `termkit`, `termkit/layout`, `termkit/progress`,
  `termkit/pick`, `termkit/notify` and `termkit/screen`, and each one is a Buzz module
  under the same path (`import "termkit/progress"`). The Buzz bindings are generated from
  the Go source at build time; a test fails when an export has none.
- **24 loaders**, each with a name, an interval and a fixed footprint, drawn as a pure
  function of elapsed time. `log.loader` picks the run band's; the default is
  `glyph/dots`, and a locale that is not UTF-8 draws `glyph/line`.

### Changed

- **The run band says `ok` and `FAIL` instead of `✓` and `✗`**, in a four-column slot,
  on its status row and on each failing project. The spinner, tree branches and `▸` stay.
- `internal/interactive/tty` and `internal/interactive/screen` are gone, which breaks
  Go importers; their code is `libs/termkit` and
  `libs/termkit/screen`. `tty.Pick` is `pick.One`, `tty.Notifier` is `notify.Stack`,
  `screen.Screen` is `screen.Terminal`, `tty.Cols`, `tty.Fit` and `tty.ClipCols` are
  `layout.Cols`, `layout.Fit` and `layout.Clip`, and `MakeRaw` returns a `RawMode` to
  `Restore`.
- `magus doctor` no longer names the band's border, which the band has not had since it
  lost its frame; a locale that is not UTF-8 now reports the ASCII spinner.
