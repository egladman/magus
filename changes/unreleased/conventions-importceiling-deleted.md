### Changed

- **`importceiling` is deleted.** The ceiling on `cmd/magus`'s imports is the `dep-fanout` `cmd/magus` threshold in `hack/policy/conformance.jsonl`, which counts every workspace import rather than `internal/` ones alone.
