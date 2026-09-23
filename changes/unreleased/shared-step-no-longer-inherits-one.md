### Fixed

- **A shared step no longer inherits one caller's timeout.** It runs under the
  invocation's cancellation. MGS3012 lists what was still admitted.
