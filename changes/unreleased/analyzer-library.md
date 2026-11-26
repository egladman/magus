### Changed

- **Breaking: `magus buzz hack/lint/run.buzz` is now `magus buzz analyzers.buzz`.** The
  runner, the analyzer record, the finding and the `analyzers.yaml` reader live in
  `libs/analyzer`, and `analyzers.buzz` lists this repository's rules. `--rule <name>` is
  now `--only <name>`, `--record` is now `--firings`, and a passing run prints nothing.
