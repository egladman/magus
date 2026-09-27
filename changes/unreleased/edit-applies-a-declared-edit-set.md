### Added

- **`magus edit` applies a multi-file edit set.** The set is JSON on `--stdin`: sites
  anchored by `lines` or `text`, each with the bytes that replace it. Every site is
  checked against the file on disk before anything is written, one bad site refuses
  the whole set with every reason listed, and a declared output is refused. Every
  file is written or none is. The receipt under the cache dir holds the undo set:
  `magus edit --undo <id>`. `--check` writes nothing; `--schema` prints the shape.
