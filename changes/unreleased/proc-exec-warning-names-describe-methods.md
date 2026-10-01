### Changed

- **The `proc\exec` warning names the `magus\describe` methods.** Running the magus binary
  through `proc\exec` for `describe <noun>`, `ls` or `ls targets` now points at
  `magus\describe.<noun>`, `magus\describe.project` or `magus\describe.graph`.
