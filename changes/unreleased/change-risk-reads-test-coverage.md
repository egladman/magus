### Changed

- **Breaking: `mgs_getCoverage` declares `{format, profile}`, with no alias.** The
  project target that declares the profile as an output writes it, and change risk
  reads it: at head that target is freshened through the cache, so ci tests the tree
  once. The reserved `coverage` op, `MAGUS_COVERAGE_DIR`, the coverage `command`,
  `file`, `test` and `uses` fields, and the 7 minute budget are removed. A spell that
  still declares the old fields fails to load: move the report path to `profile` and
  declare that path as an output of the target that writes it.
- **Breaking: change risk judges against the base only through main's CI cache.** The
  base profile is the cache entry main's CI wrote for the declaring target at the
  merge-base commit, keyed by that commit's own inputs; magus never runs the base. No
  entry is an error that names the base commit and the command. Main's CI keyed this
  change's own base before the contract changed, so the
  `touched-functions-keep-change-risk-under-10` rule lands unwired: `hack/lint/run.buzz`
  does not list it and `lint_rules` does not warm it. The follow-up is that one line and
  the warm, once main's CI has run `magus run test .` and `magus run buzz-test .` at a
  merge base that includes this change.
