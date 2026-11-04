### Changed

- **`importceiling` is deleted.** The ceiling on `cmd/magus`'s imports is the `dep-fanout` `cmd/magus` anchor in `magus.yaml`, which counts every workspace import rather than `internal/` ones alone.
