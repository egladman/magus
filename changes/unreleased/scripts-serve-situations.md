### Changed

- **A workspace script declares what it serves, and the guard names it by that.** A
  script's `final SUMMARY` and `final SERVES` list name the rules, checks and command
  shapes it answers; `magus graph build` indexes them. A deny names the scripts serving its
  rule, and `magus doctor` fails on an indexed script that is gone.
