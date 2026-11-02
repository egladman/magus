### Fixed

- **OpenCode harness installation includes its guard runtime.** Magus embeds the minimal JavaScript entry, Go WebAssembly runtime, Buzz program, and required helpers. Installation preserves binary bytes and unrelated settings, protects local asset edits, and verifies activation. Harness spells declare `mgs_getHarness`; computed changes are `HarnessInstallation`.
