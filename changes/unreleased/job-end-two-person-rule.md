### Changed

- **Ending a job no longer ends the jobs below it unseen.** `magus job exit <job>` refuses
  while jobs sit below it, naming each with its holder, checkout and uncommitted work,
  until you name each. `magus job prune` refuses likewise. If a named job holds
  uncommitted work or a running check, the end waits until a person disposes the attention
  request.
