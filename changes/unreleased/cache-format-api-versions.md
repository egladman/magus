### Changed

- **Breaking: every cache, bytecode, symbol and index format number is a version string.**
  The cache key version, the output bundle and spell cache schemas, the provider and
  lifecycle cache versions, the Buzz bytecode and bytecode record versions, the symbol
  pack, occurrence and indexer formats, the knowledge index, routing, lexical, probe,
  stamp, base measure and VCS history formats, the textrank index and weights, and the
  embed index and table formats are named `magus.<format>/v1alpha1` (`gopherbuzz.` and
  `embed.` where the module cannot import `libs/apiversion`, which compares them by
  equality). The hash salts and file magic that carried a number carry the string too.
- **The output descriptor's `key_version` member is a string.** A record that still holds
  the number is refused when read.
- **Old caches miss once and rebuild.** Nothing migrates; a build written under a number
  is never read. The `embed.table/v1alpha1` format needs the embedding table retrained and
  republished: the published table is in the old format and is refused until it is.
