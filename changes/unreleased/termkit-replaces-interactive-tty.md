### Changed

- **The run band says `ok` and `FAIL` instead of `✓` and `✗`**, in a four-column slot,
  on its status row and on each failing project. The spinner, tree branches and `▸` stay.
- **`internal/interactive/tty` and `internal/interactive/screen` are gone**, which breaks
  Go importers; their code is `libs/termkit` and
  `libs/termkit/screen`. `tty.Pick` is `pick.One`, `tty.Notifier` is `notify.Stack`,
  `screen.Screen` is `screen.Terminal`, `tty.Cols`, `tty.Fit` and `tty.ClipCols` are
  `layout.Cols`, `layout.Fit` and `layout.Clip`, and `MakeRaw` returns a `RawMode` to
  `Restore`.
- **`magus doctor` no longer names the band's border**, which the band has not had since it
  lost its frame; a locale that is not UTF-8 now reports the ASCII spinner.
