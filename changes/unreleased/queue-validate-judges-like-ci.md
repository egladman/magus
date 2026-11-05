### Fixed

- **`queue validate` judges a change as CI does.** It takes `--env`, as `queue gate` does,
  so CI's `TZ` reaches the gate. A red gate that ran on a full disk, exited 75 or took a
  SIGKILL waits with `WAIT_MACHINE_FAULT` for a retry, not `KICK_RED`. After three waits in
  a row it kicks back, naming the fault.
