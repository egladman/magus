### Added

- **The broker records its admissions in `admissions.jsonl`.** Each MGS3009 refusal
  and each claim seated after waiting on other runs appends a `MachineAdmission`
  with its outcome, wait, bound, sizing and the holders at the verdict. It sits
  beside `broker.log` and keeps the newest 5000. `server status` still shows the
  last 20 refusals.
