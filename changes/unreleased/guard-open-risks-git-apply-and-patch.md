### Fixed

- **`git apply` and `patch` are held to a job's write paths.** The guard reads the patch
  a line names, on a redirect, in a here-document or through a `cat`, and grades each file
  it writes. A worker's patch from any other pipe, an expanded word or a file the line
  itself creates is refused, since nothing shows where it writes.
