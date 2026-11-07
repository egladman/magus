### Fixed

- **A charm keys only the steps that read it.** Before, only `rw` and `update` were left
  out of a step that cannot read them; `gha`, `cd`, `extended` and every workspace charm
  keyed every step. A generator reached by `ci:gha` now replays the entry a plain
  `generate` stored, while a step whose chain reads the charm still keys on it.
