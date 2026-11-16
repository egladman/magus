### Changed

- **Breaking for SDK callers: secrets live in the `libs/secrets` module.** `types.Secret`,
  `NewSecret` and `SecretGrant` are `secrets.Secret`, `New` and `Grant`, and
  `internal/secret` is gone: its resolver is `secrets.NewResolver`, with `Provider`,
  `Redact`, `RedactString`, `NewRedactingHandler` and the endpoint forwarder beside it.
  `OpenEndpoint` takes its scope as an argument. A spell-backed provider resolves only
  through `magus.WithSecrets(secrets.WithOpener(...))`, which the CLI passes; a library
  caller that selects one without it gets an error on the first read. A refused grant is a
  `*secrets.GrantError`, still reported as MGS1027. A resolver reads through one
  provider: `Resolver.WithProvider(name)` derives one that reads through another and shares
  its memo and redaction set, and nothing selects a provider in place. The context helper
  is `secrets.WithResolver` and `DefaultTimeouts` is a function. Every error starts
  `secrets:` and is typed: `*ReadError`, `*EndpointError` (wrapping `ErrNoScope`) and
  `*NoResolverError`. An IPv6 grant host keeps its brackets (`[::1]`). No alias remains;
  change the import and the name. Buzz names are unchanged.
