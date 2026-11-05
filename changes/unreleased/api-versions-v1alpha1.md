### Changed

- **Breaking: every versioned format is named `magus.<format>/v1alpha1`.** The integer
  `schema_version` member is now `api_version`, a string such as `magus.job/v1alpha1`.
  Versions order `v1alpha1` < `v1alpha2` < `v1beta1` < `v1`, and everything starts at
  alpha. This covers job rows, declarations and results, `query output -o jsonl` records,
  knowledge graph exports and query results, guard verdicts, notify events, harness
  descriptors, agent trail events, the broker hello, the release index, the spell lock,
  installed tool rows and skill stamps, the token store, session records, the vcs owed
  record and the workspace cache. The merge queue ids become `mergequeue.<kind>/v1alpha1`. A reader refuses any other version and names the fix.
- **Every install from before this change needs one manual reinstall.** Its
  `magus self update` refuses the new release index. Run
  `curl --proto '=https' --tlsv1.2 -sSf https://eli.gladman.cc/magus/install | sh`.
- **Rebuild every checkout's `./magus` after this lands.** A job store row stamped with an
  integer still reads as `magus.job/v1alpha1` and gets the name on its next rewrite. An
  older magus cannot read a row this one wrote. Any other old store is refused: move it
  aside or delete it (`jobs.jsonl`, `tokens.d`), reinstall agent files with
  `magus agent install` and `magus agent harness install`, and relock with
  `magus spell lock --update`.
