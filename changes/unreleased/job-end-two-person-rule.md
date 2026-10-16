### Changed

- **Ending a job no longer ends the jobs below it unseen.** `magus job exit <job>` refuses
  while live jobs sit below it, naming each with its holder, checkout and uncommitted work,
  until the call names each. `magus job prune` refuses likewise. Ending one holding
  uncommitted work or a running check waits until a person disposes its attention request.
- **`magus job exit --stdin` refuses a result while a job below is still working.** Collect
  or end each one first.
- **An ended job's checkout is cleaned before magus decides whether it holds work.** Files
  magus wrote in it (declared outputs, maintained files, gofmt-only Go changes) are
  restored, and a checkout kept for real work names the uncommitted paths. A job ended by
  name also has its linked checkout restored and tried for removal, which says why it
  stays.
