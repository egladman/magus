### Security

- **Sandbox path checks follow symlinks the way the kernel does.** A `..` after a
  symlink, a write through a dangling link, and `fs\symlink` to a path outside the
  policy no longer pass the binding-level check.
