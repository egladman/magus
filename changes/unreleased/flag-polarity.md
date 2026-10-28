### Changed

- **A bool flag is declared once by its positive name, and `--no-<name>` is generated.**
  Help prints `--[no-]name (default: on|off)` from the table the man page uses, with
  a double dash, so `-symbols` and a backquoted word read as a value name are gone.
  `graph build` now declares `--symbols` and `--sessions` (on by default) like
  `graph export`, `graph stats` and `status` (off by default); `--no-symbols` and
  `--no-sessions` still parse. `describe target` names its flag `--cache-key`, with
  `--cache` as a second spelling, so `--cache` and `--no-cache` mean one thing on
  `run` and `affected`. A flag table lists each concept with its magus.yaml key, Go
  field and Buzz field, and a test fails when a surface spells one differently.
- **`--no-<name>` takes no value.** `--no-cache=false` is refused and names `--cache`.

### Removed

- **`graph export --static`**, the deprecated alias of `--reproducible`.
