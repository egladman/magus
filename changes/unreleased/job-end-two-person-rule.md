### Changed

- **Ending a job no longer ends the jobs below it unseen.** `magus job exit <job>` refuses a
  job with live jobs below it, naming each with its holder, its checkout and the uncommitted
  work it holds, until each is named in the same call. `magus job prune` refuses the same
  way for a job it would end only because an ancestor ends, until it is restated as an
  argument. When a job ended this way holds uncommitted work in its write paths or a running
  check, the end also waits on a person to dispose the attention request the refusal names.
- **`magus job exit --stdin` refuses a result while a job below is still working.** Collect
  or end each one first.
- **An ended job's checkout is cleaned before magus decides whether it holds work.** Files
  magus wrote in it (declared outputs, files magus maintains, Go files only gofmt changed)
  are restored, so they no longer keep the checkout, and a checkout kept for real work names
  the uncommitted paths. A job ended by name also has the linked checkout it was taken in
  restored and tried for removal, which says why it stays.
