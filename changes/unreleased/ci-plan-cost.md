### Changed

- **`magus affected ci --plan` plans faster.** It evaluates each project's `ci`
  concurrently, and packing 2000 units takes about 7ms instead of 480ms.
