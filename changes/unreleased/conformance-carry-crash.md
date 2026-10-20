### Fixed

- **A conformance ratchet that lists one item twice under a metric is refused.** It used to
  crash `lint` and `format` when only one of the two entries recorded the item's shape and
  the item was renamed or moved. The error names both lines.
