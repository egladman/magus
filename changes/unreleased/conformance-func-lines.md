### Changed

- **This repository commits `func-lines` thresholds learned from its own code.** 52 lines for the `source` population and 75 for `main`, the lens's p95 at 70685c0d7, replace the shipped anchors of 92 and 140. The 845 functions past them, and 132 test functions past the test anchor, are known entries that only shrink; lint fails an unlisted function past its value.
