### Added

- **`magus session` records a run the machine refused.** A step turned away because the
  machine's build budget was full (MGS3009) is now a failed target in the session store,
  with its code, so `magus session` and `magus session --brief` show the refusal instead
  of an empty history. A refusal has no output ref because nothing ran.
- **`magus session --all`** lists invocations from every checkout of the repository.

### Changed

- **`magus session` lists this checkout's invocations by default.** The store is shared by
  every worktree of a repository, so the bare listing used to mix them. Invocations
  recorded before the checkout was stored are still listed. `magus session --brief` takes
  its last-run failures from this checkout only.
