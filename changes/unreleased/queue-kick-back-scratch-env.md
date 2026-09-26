### Added

- **`--cache-env NAME=DIR` on `magus queue validate` and `magus queue apply`** points
  a variable at a directory inside each candidate checkout's `.magus`, so cache isolation
  lives on the queue's flags rather than on the hook's command line. It may repeat.
