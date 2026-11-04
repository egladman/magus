### Changed

- **The git hooks installer removes shims whose script left the directory.** Install
  deletes a shim it wrote for a `<hook>.buzz` that is gone and logs each removal;
  hooks it did not write stay. A shim whose script is missing still exits 0 but
  names the script on stderr.
