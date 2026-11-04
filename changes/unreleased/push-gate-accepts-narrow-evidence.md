### Changed

- **`push-ungated` admits a push that passing narrow runs cover.** Every file changed
  since the last green gate needs a passing run of a target whose inputs hold it; a file
  the host cannot build is left to CI and named, and an uncovered file is refused with
  the command that would cover it.
