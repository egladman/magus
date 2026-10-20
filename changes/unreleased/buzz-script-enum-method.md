### Fixed

- **A script run by `magus buzz` can call a method that returns an enum.** The host read the enum with a top-level `return`, which the strict session rejects; this repo's `hack/dev/rename-symbol.buzz` test failed on it.
