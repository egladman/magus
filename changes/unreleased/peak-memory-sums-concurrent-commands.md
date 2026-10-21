### Changed

- **A step's recorded peak memory now sums commands that run at once.** Each
  command's process tree is sampled with its time, and the step's peak is the largest
  total across its commands at one time, never less than the largest figure the kernel
  reported for one command. Two commands started together were recorded as the larger.
