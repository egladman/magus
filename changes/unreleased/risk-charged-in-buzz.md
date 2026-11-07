### Added

- **`ChangeRiskFunction.charged(threshold)` and `FunctionGap.decided(threshold)` in the `magus` module.** A
  lint rule passes its own threshold and gets magus's verdict: charged is past threshold and worse than at base,
  and decided means the gap's worst case holds within threshold.
