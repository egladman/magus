### Changed

- **A machine short of memory now refuses a new step.** While the kernel reports memory
  pressure, a step exits 75 with MGS3053 instead of starting: on macOS the compressor at
  75% of its segment limit or critical pressure, on Linux a full memory stall for 25% of
  the last 10 seconds. Running steps keep going. `broker: off` turns it off.
