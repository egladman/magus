### Changed

- **Breaking: every cache, bytecode, symbol and index format number is a version string.**
  The cache key version, bundle and spell cache schemas, provider and lifecycle cache
  versions, symbol pack and indexer formats, knowledge index
  formats, textrank formats and embed formats are named `magus.<format>/v1alpha1`.
- **Modules that cannot import `libs/apiversion` use their own group.** `embed.` formats
  compare by equality. Hash salts and file magic that carried a number
  carry the string too.
- **The gopherbuzz bytecode versions stay numeric.** `BytecodeVersion` and
  `bytecodeRecordVersion` follow upstream Buzz, the one exception to the version-string rule.
- **The output descriptor's `key_version` member is a string.** A descriptor that still
  holds the number is refused when read.
- **Old caches miss once and rebuild.** Nothing migrates, and an entry written under a
  number is never read.
- **The published embedding table needs retraining.** A table in the old numbered format
  is unavailable, not an error: one warning names it and the retrain and publish commands,
  and ranking skips the embed list as it does with no table. A newer format is refused.
