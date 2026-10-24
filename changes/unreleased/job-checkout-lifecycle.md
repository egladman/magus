### Changed

- **Breaking: a `job fork --checkout` lands on a branch named after the job.** git, hg and
  jj each name it; a taken name is refused. Job ids are therefore lowercase letters,
  digits, `-`, `.` and `/` between segments, and the refusal names the rule. `job rm` ends
  the checkout its row owns unless it holds work.
