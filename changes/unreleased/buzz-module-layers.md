### Changed

- **Breaking: the disk modules are imported as `magus/diskwatch`.** `import "diskwatch";`
  is now `import "magus/diskwatch";`, the way `magus/reclaimdisk` already imports. The
  module is unchanged, and it still imports no magus module. A script or hook that
  names the bare path fails with `module not found`.
