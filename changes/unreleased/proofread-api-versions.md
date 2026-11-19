### Changed

- **Breaking: proofread's baseline, outcomes and voice files carry `api_version` strings.**
  `proofread.baseline/v1alpha1`, `proofread.outcomes/v1alpha1` and
  `proofread.voice/v1alpha1` replace the numeric `version` and the voice `schema`. A numeric
  baseline or outcomes file is refused. A voice file with `schema` is set aside with a
  warning, and `proofread voice build` rebuilds it.
