### Changed

- **Breaking: `magus session show -o json` counts `shadow` under `decisions`.** Each profile's verdicts are keyed by verdict, in place of `allow`, `advise` and `deny`, so a verdict outside the three is counted rather than read as an allow.
