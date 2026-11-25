### Changed

- **A job-store line with fields this magus does not know reports MGS3047, not MGS3050.**
  The line decodes and is kept for the build that wrote it. The error names the field
  paths, the reading binary and its version, and the relink line.
  [MGS3047](https://eli.gladman.cc/magus/reference/codes/sandbox/MGS3047/).
