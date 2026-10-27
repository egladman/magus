### Fixed

- **Ending a job removes only a checkout its fork made.** A row records `made` when its fork
  created the checkout. A checkout the fork adopted, one a job was only taken in, and one a
  row recorded before `made` existed stand after `job exit`, `job wait`, `job prune` and
  `job rm`, which say why.
