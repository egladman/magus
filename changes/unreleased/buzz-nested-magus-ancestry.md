### Fixed

- **A magus that a `magus buzz` script starts knows its parent.** The script's own
  invocation now joins `MAGUS_INVOCATION_ANCESTORS`, so a nested `magus run` is no longer
  refused MGS3009 as blind to its ancestry when the build budget is contended.
