### Added

- **`std.WithLanguages` and `std.LanguagesFromContext`.** A host module reads the
  languages the workspace's resolved spells declare from the context
  `Magus.ApplySandbox` returns, so a script, the MCP worker and a target see the same
  index. Generated module declarations import any host module they name.
