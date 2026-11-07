### Changed

- **`magus status` no longer prints a `kind` for each waiting claim.** The status JSON drops `kind` from its `waiting` entries too, since every waiter was a claim.
