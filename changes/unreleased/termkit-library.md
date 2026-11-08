### Added

- **termkit, the terminal UI library magus draws with, is public.** `libs/termkit` is
  its own Go module with packages `termkit`, `termkit/layout`, `termkit/progress`,
  `termkit/pick`, `termkit/notify` and `termkit/screen`, and each one is a Buzz module
  under the same path (`import "termkit/progress"`). The Buzz bindings are generated from
  the Go source at build time; a test fails when an export has none.
- **24 loaders**, each with a name, an interval and a fixed footprint, drawn as a pure
  function of elapsed time. `log.loader` picks the run band's; the default is
  `glyph/dots`, and a locale that is not UTF-8 draws `glyph/line`.
