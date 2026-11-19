### Changed

- **Guard template, glue and coverage markers carry api versions.** `magus-guard-template` and the glue stamp read `magus.guard-template/v1alpha1`, coverage lines read `api_version=magus.guard-coverage/v1alpha1` in place of `schema=2`, and the OpenCode ABI reads `magus.opencode-wasm/v1alpha1`. A copy still numbered is refused by `magus doctor`: run `magus agent harness install`.
