### Changed

- **MGS3044 also covers a file a target rewrites in place.** A target that declares
  `editsExistingSource: true` over a file outside a job's write paths is refused like a
  regenerated output, and the message says "rewrites in place".
  [MGS3044](https://eli.gladman.cc/magus/reference/codes/sandbox/MGS3044/).
