### Changed

- **BREAKING: the in-repo analyzers carry no repository policy, and their settings
  are renamed.** `hostagnostic` takes its host list from `hosts` and `host-patterns`,
  and six conventions analyzers take a `hint` for the repository-specific half of
  their message. `providerio`'s `dirs`, and the `skip-dirs` of `filenames` and
  `hostagnostic`, must each match Go files in the tree, or the linter fails to load.
  `stutter` reads the package clause, skips `package main`, and defaults
  `min-package-len` (was `min-package`) to 3. Renamed keys: `nameoutput` `case` is
  `case-ident`; `coldread` `wrapped` is `report-wrapped`; `testlayout` `unpaired`,
  `no-unix-suffix` and `no-main-tests` are `report-unpaired`, `report-unix-suffix`
  and `report-main-tests`, and `ignore-marker` becomes `honor-marker`, off by
  default, so `// cross-cutting:` excuses nothing unless a config opts in.
