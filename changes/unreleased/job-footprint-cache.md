### Changed

- **Footprints are cached per checkpoint.** `job wait`, `describe job` and `ls jobs` reuse the changed regions of a checkout whose files have not changed, and `ls jobs` reuses a row's whole footprint while the row and its checkout's HEAD and uncommitted state are unchanged.
