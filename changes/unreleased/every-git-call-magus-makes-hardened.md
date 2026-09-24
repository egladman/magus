### Fixed

- **Every git call magus makes is hardened the same way.** `GIT_DIR`, `GIT_REPLACE_REF_BASE`,
  `GIT_ATTR_SOURCE`, the shallow and pathspec variables and injected config never reach
  git, including the shallow-clone deepening fetch, which re-added them. git never prompts
  for credentials, and no signing, rerere or signature line changes what magus reads.
