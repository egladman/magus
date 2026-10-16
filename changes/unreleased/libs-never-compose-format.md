### Changed

- **Breaking: `libs/testlayout`, `libs/scipbuzz` and `libs/cmdline` no longer rewrite files outside `ci`.** Their `lint`, `build` and `test` targets stop needing `format`, and each `ci` runs `format` before them. Run `magus run format <project>` yourself before any of them alone.
