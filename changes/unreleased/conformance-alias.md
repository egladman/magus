### Added

- **`magus diff` reads a forced import alias as a naming smell.** The `import-alias` conformance check, on by default as advice, names an aliased import of a package its importers rename past the configured value, and a new package that takes an existing package's name. Remedies: rename the package, or add a known entry with the reason.
