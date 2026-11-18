### Changed

- **Breaking: every cache, bytecode, symbol and index format number is a version string.**
  The cache key version, bundle and spell cache schemas, provider and lifecycle cache
  versions, Buzz bytecode versions, symbol pack and indexer formats, knowledge index
  formats, textrank formats and embed formats are named `magus.<format>/v1alpha1`.
- **Modules that cannot import `libs/apiversion` use their own group.** `gopherbuzz.` and
  `embed.` formats compare by equality. Hash salts and file magic that carried a number
  carry the string too.
- **The output descriptor's `key_version` member is a string.** A descriptor that still
  holds the number is refused when read.
- **Old caches miss once and rebuild.** Nothing migrates, and an entry written under a
  number is never read.
- **The published embedding table needs retraining.** It is in the old `embed.table`
  format and is refused until a table in the new format is published.
