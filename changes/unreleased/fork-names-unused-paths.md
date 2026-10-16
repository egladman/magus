### Changed

- **A fork refused over another job's file names what that job has not used.** The
  MGS3032 and MGS3046 refusals name the holder, how long since it wrote, and the write paths
  its diff since its checkpoint has not touched, with the `magus job apply -f <record>` that
  narrows it and the `--depends-on` that waits for it. Each refusal also files a release
  request listing those paths, and a dry run still files none.
- **`magus job exit` with a result narrows the job to what it changed.** Write paths that
  cover no path in the result are recorded as releases, so an exited job awaiting
  `magus job wait` no longer holds files it never touched. An exit with no result keeps
  every path.
