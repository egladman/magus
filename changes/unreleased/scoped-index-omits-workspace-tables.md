### Changed

- **`magus describe graph -o markdown <project>` renders only that project.** A scoped
  index drops the workspace-wide kind and project tables, so a change elsewhere cannot
  make it stale; the unscoped index keeps them. A project path that names no project is
  now an error instead of an empty index.
