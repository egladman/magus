### Added

- **`use` stitches another magus workspace in by local path.** A `magus.yaml` entry
  `{name, path, share}` lends this workspace the used one's lint rules. A target reads its
  files as `@<name>/<glob>`, hashed into the key under that name, and a magusfile imports
  its modules as `import "@<name>/<path>"`. Each used root is a read-only sandbox entry.
  `magus describe workspace` and `magus\describe.workspace()` list the entries. An entry
  naming no workspace stops the load (MGS1054).

### Changed

- **A read pattern reaching outside the workspace is refused.** `ctx.readsFiles` with a
  `..` past the root or an absolute path used to key nothing, silently. It now fails the
  run and names `use`.
