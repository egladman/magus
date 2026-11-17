### Fixed

- **`--schema` honors `-o` on `magus job fork`, `job exit` and `job wait`.** `-o name`
  prints the top-level field names and `-o template` only what it asks for, so a reader
  after the fields skips the whole document.
- **`magus describe file` names each overlapping set of paths once.** The declarations
  covering it list under the set, not one repeat of it per declaration.
