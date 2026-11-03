### Changed

- **`ns\object\member` is rejected, as upstream rejects it.** A namespace object's
  members take a dot: `magus\workspace().jobs()`, `magus\secret.read(ref)`. The error names the
  dot form.
