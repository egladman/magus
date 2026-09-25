### Changed

- **Breaking for SDK callers: `types.RegionReporter` gains `RegionsBetween` and `Drivers`.**
  `RegionsBetween` places the lines two in-memory versions of a file differ in, and
  `Drivers` names each path's diff driver. A backend implementing `types.VCSDriver` adds
  both, or declines with `*types.VCSUnsupportedError`.
