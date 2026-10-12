### Added

- **`proofread calibrate round` runs a labeling round you can rerun.** `sample` draws
  firings from a seed, `freeze` fixes the rule commit and gates before anyone labels, and
  `score` reports precision, the Wilson lower bound and Krippendorff's alpha per rule and
  source. The guideline and a text-free record of rounds 1 to 4 are committed.
