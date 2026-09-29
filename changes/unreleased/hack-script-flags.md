### Changed

- **`hack/registry.buzz` is now `hack/toolchain-releases.buzz`, and `hack/gha-queue.buzz`
  is now `hack/ci/merge-queue.buzz`.** hack/ci/ holds the steps only a workflow runs.
- **`hack/toolchain-releases.buzz` and `hack/host-schemas.buzz` take steps and flags, not
  environment variables.** Run `magus buzz hack/toolchain-releases.buzz -- fetch | build | verify`
  with `--upstream`, `--releases`, `--out`, `--expires` and `--no-sign`, and
  `magus buzz hack/host-schemas.buzz -- verify | fetch`. Neither has a default step: a
  bare run prints the usage line and fails, so nothing signs the registry unless `build`
  is named. `MAGUS_REGISTRY_KEY` stays an environment variable because it is a secret.

### Added

- **`hack/lint.buzz` lints named files or one rule.** `-- <path>...` reports only the
  findings at those files, from the rules that read them; a path no rule reads is not an
  error. `-- --rule <name>` runs one rule and refuses an unknown name, listing the rules.
  A bare run still lints the whole tree.
