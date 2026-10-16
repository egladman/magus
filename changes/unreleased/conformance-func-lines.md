### Changed

- This repository commits `func-lines` thresholds learned from its own code: 52 lines for the `source` population and 75 for `main`, the lens's p95 at 70685c0d7, in place of the shipped anchors of 92 and 140. The 845 functions past them are a known entry that only shrinks, and `func-lines` stays advice in lint.
