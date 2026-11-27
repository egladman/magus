### Fixed

- **`magus buzz --check` and `magus buzz` resolve a project-relative import from the workspace root.** A script under a project with its own magusfile, such as `docs/`, took that project as the root, so `import "hack/lib"` failed. Both now use the workspace root.
