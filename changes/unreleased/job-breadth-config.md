### Added

- **The write-path breadth bounds are config keys.** `jobs.max_claim_files` (default 50)
  and `jobs.max_claim_projects` (default 1) set how many files and projects one glob write
  path may reach before `magus job fork` refuses it as too broad. Zero or negative is an
  error, and the keys read the workspace's own `magus.yaml` only.
