### Changed

- **Spell helpers return one Result.** Helpers in the S3 cache, TypeScript, Python, Go
  module and search spells return a `result\Result`, and only a host-called entry
  still raises. In `spells/code/search`, `matches`, `around` and `run` return one, and
  its `Result` object is now `Report`. A Python invalid-TOML gap now quotes the parser.
