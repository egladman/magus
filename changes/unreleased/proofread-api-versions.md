### Changed

- **Breaking: proofread's baseline, outcomes and voice files carry `api_version` strings.**
  `proofread.baseline/v1alpha1` and `proofread.outcomes/v1alpha1` replace the numeric
  `version`; the voice schema is `proofread.voice/v1alpha1`. The SARIF fingerprint key
  changes, so every finding reads as new once. A numeric baseline is refused: rerun
  with `--baseline FILE --prune`. A numeric outcomes file is refused: delete it.
