### Changed

- **Breaking: `libs/diagnostics`, `libs/xdg`, `libs/conventions`, `libs/gopherbuzz` and `libs/proofread` no longer rewrite files outside `ci`.** Their `lint`, `build` and `test` targets stop needing `format`, and each `ci` runs `format` before them. Run `magus run format <project>` yourself before any of them alone.
