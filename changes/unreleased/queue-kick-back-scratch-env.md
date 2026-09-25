### Added

- **`--scratch-env NAME=DIR` on `magus queue validate` and `magus queue apply`** points
  a variable at a directory inside each candidate's scratch space, so cache isolation
  lives on the queue's flags rather than on the hook's command line. It may repeat.
