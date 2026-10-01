### Removed

- **`magus-utils mockassert`.** The `mocks-generate` target writes each published mock's
  interface assertions through `hack/magusfile/mockassert.buzz`, byte for byte what the
  Go subcommand wrote, and the guard no longer admits the subcommand as a recovery
  command for a checkout that cannot load.
