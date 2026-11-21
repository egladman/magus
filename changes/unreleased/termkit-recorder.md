### Added

- **TermKit exposes a Go recorder through its generated Buzz bindings.** It splits
  terminal captures into frames and renders animated SVGs.

### Changed

- **Breaking: generated record methods belong to Buzz objects.** Calls use
  `line.spansCopy()` instead of `termkit\lineSpansCopy(line)`. Generated map decoders
  also refuse unknown fields instead of ignoring them.
