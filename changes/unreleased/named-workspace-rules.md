### Added

- **A workspace guard rule can name its verdict.** `magus\guard.deny` and `advise` take
  `{"rule": "pull-request-text"}`, so the verdict reports as `workspace:pull-request-text`, a
  repeat shortens, and the trail tells it apart from other workspace rules. A name that is not
  kebab-case, or is `command`, `write` or `spawn`, raises MGS1045.
