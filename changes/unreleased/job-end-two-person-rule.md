### Changed

- **Ending a job no longer ends the jobs below it unseen.** `magus job exit <job>` refuses
  while live jobs sit below it, naming each with its holder, checkout and uncommitted work,
  until the call names each. `magus job prune` refuses likewise. Ending one holding
  uncommitted work or a running check waits until a person disposes its attention request.
