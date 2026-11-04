### Changed

- **Declare command output support in the command tree.** Commands that own raw, streaming, or protocol output now declare that beside their flags and help. Both forms of `-o` validation read the declaration, replacing a separate exclusion list. Subcommands declare their own behavior.
