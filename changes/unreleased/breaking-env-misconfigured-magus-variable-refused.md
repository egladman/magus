### Changed

- **Breaking: a retired or misspelled `MAGUS_*` variable stops every command.** Setting
  `MAGUS_NO_WAIT`, a `MAGUS_DAEMON_*` name, or a near miss such as `MAGUS_CACHE_DIRR`
  fails with MGS1046 before any work, naming the fix; `magus shell` denies instead.
  Other unknown names run, and `magus doctor` reports them.
