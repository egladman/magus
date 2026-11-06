### Changed

- **Breaking: a target that writes a file it does not declare fails with
  [MGS4011](https://eli.gladman.cc/magus/reference/codes/race/MGS4011/).** After a run,
  magus names each file in the project it created, or untracked file it modified, that no
  target declares. A write made while a related target was running is logged, not blamed.
  A target that declares no outputs and passes is a check.
- **Breaking: the `target-outputs` PR advisor and its action input are gone.** It guessed
  whether a new target with no outputs forgot them, which MGS4011 now answers at run time.
  Remove `target-outputs:` from any `with:` block that sets it.
