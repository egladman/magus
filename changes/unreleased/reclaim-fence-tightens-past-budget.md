### Fixed

- **A cache past its budget is trimmed back to it even when its files are newer than the
  fence.** When the fence alone holds a cache past its budget, the reclaim trims again at
  1h, oldest hours first, and its line says the fence tightened and from what.
  `reclaim-disk-space.buzz` names `--keep-within` as a duration such as `24h`.
