### Changed

- **Breaking: every versioned format is named `magus.<format>/v1alpha1`.** The integer
  `schema_version` member is now `api_version`, a string such as `magus.job/v1alpha1`.
  Versions order `v1alpha1` < `v1alpha2` < `v1beta1` < `v1`, and every format starts at
  alpha. A reader refuses any other version and names the fix.
- **Breaking: the formats renamed.** Job rows, declarations and results, output records,
  graph exports and query results, guard verdicts, notify events, harness descriptors,
  trail events, the broker hello, the release index, the spell lock, tool rows, skill
  stamps, tokens, session records, the owed record, the workspace cache and the merge
  queue ids (`mergequeue.<kind>/v1alpha1`).
- **Every install from before this change needs one manual reinstall.** Its
  `magus self update` refuses the new release index. Run
  `curl --proto '=https' --tlsv1.2 -sSf https://eli.gladman.cc/magus/install | sh`.
- **Rebuild every checkout's `./magus` after this lands.** A job store row stamped with an
  integer still reads as `magus.job/v1alpha1`, and an older magus cannot read a row this
  one wrote.
- **Other old state is refused, naming its fix.** Move it aside or delete it
  (`tokens.d`), rerun `magus agent install` and `magus agent harness install`, and relock
  with `magus spell lock --update`.
