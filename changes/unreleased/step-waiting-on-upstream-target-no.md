### Fixed

- **A step waiting on an upstream target no longer masks a stall.** Only a moving step
  beats the heartbeat; MGS3013 and MGS3012 still catch a wedge.
