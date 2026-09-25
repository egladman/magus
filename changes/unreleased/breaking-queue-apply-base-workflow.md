### Changed

- **Breaking: `magus queue apply` requires `--base`, and `--workflow` with a run
  source.** Verdicts no longer carry `message`, and a provider's `list_artifacts`
  returns the run's origin as `run`; a provider script without it is refused.
