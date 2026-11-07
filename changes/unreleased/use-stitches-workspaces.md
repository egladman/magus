### Added

- **`use` stitches another magus workspace in by local path.** A target reads its files
  as `@<name>/<glob>`, keyed under that name, and a magusfile imports its modules as
  `import "@<name>/<path>"`. Each used root is read-only in the sandbox, and
  `magus describe workspace` lists the entries. An entry naming no workspace stops the
  load (MGS1054).

### Changed

- **A read pattern reaching outside the workspace is refused.** `ctx.readsFiles` with a
  `..` past the root or an absolute path used to key nothing, silently. It now fails the
  run and names `use`.
