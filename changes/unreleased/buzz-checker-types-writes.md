### Fixed

- **The Buzz checker types writes into fields, lists and maps.** A wrong value
  written to a field, element or map key is a compile error, as upstream reports
  it. Map literals are checked against their expected type, mutability included.
  Scripts that wrote a wrong value now fail `magus buzz --check`.
