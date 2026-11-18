### Changed

- **Breaking: proofread's baseline, outcomes and voice files carry `api_version` strings.**
  A baseline is `proofread.baseline/v1alpha1` and the outcomes file
  `proofread.outcomes/v1alpha1`, each under an `api_version` key in place of the numeric
  `version`. A voice file is `proofread.voice/v1alpha1`, and the numbered
  `proofread-voice/N` schemas are refused with the rebuild command. The SARIF
  fingerprint key is `proofread.fingerprint/v1alpha1`, so a code scanning service sees
  every finding as new once. A baseline in the numeric form is refused with its path;
  rewrite it with `--baseline FILE --prune`. An outcomes file in the numeric form is
  refused; delete it and `--record` starts the counts again.
