### Fixed

- **A fork inside a job's checkout refuses a checkpoint that is not its head.** The fork
  would adopt that checkout and grade the job at its head, not the commit the checkpoint
  names. The refusal names `--checkout <dir>`, the checkout the work is in.
