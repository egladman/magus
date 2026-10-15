### Fixed

- **`magus job exit` on a job that already ended says so.** The error names how it ended
  and how long ago, instead of a refusal about writing another caller's row, or no error
  and a rewritten end.
- **A foreground spawn's worker is bound to its job on its first call.** A host that sends
  no start for a foreground child no longer leaves it refused by `sibling-checkout` in the
  checkout its job owns.
- **A script run by `magus buzz` can call a method that returns an enum.** The host read
  the enum with a top-level `return`, which the strict session rejects; this repo's
  `hack/dev/rename-symbol.buzz` test failed on it.
