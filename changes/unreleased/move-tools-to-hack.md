### Changed

- **The repository's own scripts moved from `tools/` to `hack/`, one directory per
  caller.** `hack/dev/` holds what a person runs while working, `hack/remote/` runs a
  command elsewhere, `hack/magusfile/` holds the modules magusfiles import,
  `hack/ci/` what CI and the merge queue invoke; `hack/README.md` lists them all.
  `buzz-test` runs every hack/ script's test blocks, strict
  unless the file is a magusfile import.
