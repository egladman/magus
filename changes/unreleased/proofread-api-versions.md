### Changed

- **Breaking: proofread's baseline, outcomes and voice files carry `api_version` strings.**
  `proofread.baseline/v1alpha1` and `proofread.outcomes/v1alpha1` replace the numeric
  `version`; the voice file is `proofread.voice/v1alpha1`, under `api_version` in place
  of `schema`. The SARIF fingerprint key changes. A numeric baseline is refused: rerun
  with `--baseline FILE --prune`. A numeric outcomes file: delete it.
