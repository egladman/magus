### Fixed

- **A subagent holding no job cannot write into a job's checkout through the shell.**
  `cp`, `mv`, `tee`, a redirect, or an in-place edit of an existing file there is denied
  under `lease-write`, naming the job and `magus job exec`, as the editor tool's write
  already was. A person and the root session still write there freely.
