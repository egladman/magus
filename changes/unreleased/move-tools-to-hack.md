### Changed

- **The repository's own scripts moved from `tools/` to `hack/`.** Paths keep their
  shape: `hack/advisories.buzz`, `hack/policy/guard.buzz`, `hack/lint/`. `buzz-test`
  runs every `hack/*.buzz` test block, strict unless the file is a magusfile import.
