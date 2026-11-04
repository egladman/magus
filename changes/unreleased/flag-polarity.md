### Changed

- **A bool flag is declared once by its positive name, and `--no-<name>` is generated.**
  Help prints `--[no-]name (default: on|off)` from the man page's table. `graph build`
  declares `--symbols` and `--sessions`, and `describe target` names its flag
  `--cache-key`, so `--cache` means one thing on `run` and `affected`. A test fails when
  a flag's four spellings disagree.
