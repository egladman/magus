### Changed

- **`magus job exit` ends only the jobs it names.** It no longer ends other dead rows or
  removes their checkouts on the way, `--dry-run` writes nothing, and a checkout whose
  magusfile this magus cannot load is kept with one line naming the diagnostic code.
