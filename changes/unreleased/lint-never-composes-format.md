### Changed

- **Breaking: `lint`, `build` and `test` never run `format` for you in this repo.** The
  root `lint`, `lint_rules`, `install_linter`, `build`, `test`, `compress_cgo_test`,
  `hoste2e_test` and `coverage_render` targets no longer need it. Only `ci` runs `format`,
  then `lint`, `build` and `test`; run `magus run format .` yourself before any of them
  alone.
