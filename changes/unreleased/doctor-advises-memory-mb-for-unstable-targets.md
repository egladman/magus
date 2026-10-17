### Changed

- **`magus doctor` advises declaring `memory_mb` only for an unstable target.** An
  undeclared target now claims its measured peak, so MGS1030 reports one only when its
  passing peaks spread more than a quarter apart.
