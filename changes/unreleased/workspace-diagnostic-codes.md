### Added

- **A workspace declares its own diagnostic codes.** `diagnostics: {prefix, docs}` in
  `magus.yaml` names the workspace's code prefix and the page each code has.
  `magus\diagnosticCode("WS1003")` mints a code under that prefix, and the CLI marks a
  workspace code's origin, so it never reads as one of magus's own.
- **Code prefixes cannot clash.** Each catalog magus ships registers its prefix at startup,
  and a workspace prefix that equals, starts or is started by one of them is a load-time
  error.
- **`magus doctor` checks the workspace's code pages.** The `workspace-diagnostic-docs` check
  fails a page under `diagnostics.docs` that is not a code under the prefix or names no next
  step.
- **A script serves a diagnostic by code.** `SERVES = ["code:MGS1021 -- --checkout \{checkout}"]`
  makes the MGS1021 remedy name that script, and the `{checkout}` placeholder fills in the
  checkout.

### Changed

- **`magus\raise` takes a `DiagnosticCode`, not a string.** Mint the code with
  `magus\diagnosticCode`; `raise` checks it again against the workspace prefix.
- **The `mgs:` situation family is now `code:`.**
- **The Go symbol indexer is chosen by format.** A spell declares
  `format = SymbolFormat.goPackages` with the package patterns as the command's args, in
  place of a `go list <patterns>` command.
- **A code magus does not document has no docs link.** The resolver no longer sends an
  unknown prefix to the sandbox pages.
- **Remedies name what the workspace declares.** The stale-binary advice and doctor's
  guard-binary check name scripts a workspace serves, not magus's own build targets.
