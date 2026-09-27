### Added

- **Provider I/O stays in Buzz.** A `providerio` Go analyzer (`libs/conventions`) refuses
  a `net/http` client or a provider SDK import outside a reasoned allowlist; a
  `provider-io-is-buzz` lint rule refuses a real `gh`, `curl` or `http` call at a
  provider host from a Buzz file outside the ones the boundary names.
