### Changed

- **`magus job apply -f` takes back what `magus describe job -o json` prints.** The row
  under `job` is applied and the members magus derived beside it are ignored, so describe,
  edit and apply round-trip; a nested row may leave out `schema_version`.
- **`magus job exit` ends only the jobs it names.** It no longer ends other dead rows or
  removes their checkouts on the way, `--dry-run` writes nothing, and a checkout whose
  magusfile this magus cannot load is kept with one line naming the diagnostic code.
- **Ending a job that already ended is no failure.** `magus job exit` prints
  `<id> already ended <state> <ago>` and exits 0; with `--stdin` it is still refused.
