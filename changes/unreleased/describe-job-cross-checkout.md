### Fixed

- **`magus describe job` grades a row whose checkout is another tree.** It panicked asking
  for a second workspace after the first; the row's checkout now opens its own workspace
  beside the one the command started in.
