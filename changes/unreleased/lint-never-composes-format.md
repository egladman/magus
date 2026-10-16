### Changed

- **Breaking: only `ci` rewrites files in this repo.** The root `lint`, `lint_rules`, `install_linter`, `build` and `test` targets no longer need `format`. `ci` runs `format` first, then `lint`, `build` and `test`; run `magus run format .` yourself before any of them alone.
