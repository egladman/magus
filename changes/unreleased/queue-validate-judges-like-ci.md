### Fixed

- **`queue validate` judges a change as CI does.** It takes `--env`, as `queue gate` does,
  so CI's `TZ` and cache settings reach the queue's gate. A gate red on a candidate and its
  base because of a full disk (MGS3052), a temporary failure or a kill waits with
  `WAIT_MACHINE_FAULT` and asks for a retry, not `WAIT_BASE_RED`.
