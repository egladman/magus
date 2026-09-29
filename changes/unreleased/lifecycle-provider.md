### Added

- **`magus describe tools` says when each installed toolchain's release cycle ends.** A
  magusfile wires one lifecycle provider with `magus\lifecycle.provider(<spell>)`; the
  spell exports `list_lifecycles` and returns `[Lifecycle]`. Each row gains `lifecycle`,
  `cycle`, `eol` and `support` (`supported`, `eol`, `unannounced`, `unknown`), and a
  header line names every URL the provider read. `-o json` carries a `lifecycle` object
  with its `state`: `live`, `offline`, `unreached` or `unwired`. Nothing it answers fails
  a build.
- **`spells/endoflife-date`** is the first provider, reading
  `https://endoflife.date/api/v1/products/<product>`. It ships as source, not in the
  binary: copy it into your workspace's `spells/` (docs/concepts/providers.md).
- **`magus\tools()`** returns the same report as a typed `ToolReport`.
- **The `toolchain-lifecycle` doctor check** gives advice when an installed version, or a
  project's `tools` floor, is in a cycle past its end of life. It reads the answer
  `describe tools` stored and never fetches.
- **The console's Toolchain tile** shows the cycle, end-of-life date and support, and
  says when the provider was offline or unreachable.

### Changed

- **`MAGUS_OFFLINE` also stops the lifecycle provider.** `describe tools` replays the
  stored answer, or reads `unknown (offline)`.
