### Added

- **`magus describe tools` says when each installed toolchain's release cycle ends.** A
  magusfile wires one lifecycle provider with `magus\lifecycle.provider(<spell>)`; the
  spell exports `list_lifecycles` and returns `[Lifecycle]`. Each row gains `lifecycle`,
  `cycle`, `eol` and `support` (`supported`, `eol`, `unannounced`, `unknown`). `-o json`
  carries a `lifecycle` object with its `state`: `live`, `offline`, `unreached` or
  `unwired`. Nothing it answers fails a build.
